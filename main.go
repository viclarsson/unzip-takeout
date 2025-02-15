package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/schollz/progressbar/v3"
)

var maxWorkers int
var autoMode bool
var dryRun bool
var basePath string
var logFile string

const maxRetries = 3
const assumedExtractionSpeed = 100 * 1024 * 1024 // 100MB/s extraction speed assumption
const hashThreshold = 10 * 1024 * 1024           // Only hash files smaller than 10MB

func init() {
	flag.IntVar(&maxWorkers, "workers", 4, "Number of parallel extraction workers")
	flag.BoolVar(&autoMode, "auto", false, "Skip confirmation and auto-start extraction")
	flag.BoolVar(&dryRun, "dry-run", false, "Show extraction details without performing extraction")
	flag.StringVar(&basePath, "base-path", "", "Base path within the ZIP file to start extraction from")
	flag.StringVar(&logFile, "log", "", "Path to write extraction logs")
}

// ExtractionLog represents a single file extraction attempt
type ExtractionLog struct {
	Path      string    // Path within the zip
	DestPath  string    // Destination path on disk
	Size      int64     // File size
	Status    string    // "Extracted", "Skipped", "Failed"
	Reason    string    // Why it was skipped/failed, or empty for success
	Timestamp time.Time // When the extraction was attempted
	DryRun    bool      // Whether this was a dry run
}

type ZipExtractor struct {
	workers    int
	autoMode   bool
	dryRun     bool
	destFolder string
	basePath   string
	logs       []ExtractionLog
	logsMutex  sync.Mutex // Add mutex for logs
}

func NewZipExtractor(workers int, autoMode bool, dryRun bool, destFolder string, basePath string) *ZipExtractor {
	return &ZipExtractor{
		workers:    workers,
		autoMode:   autoMode,
		dryRun:     dryRun,
		destFolder: destFolder,
		basePath:   filepath.Clean(basePath),
	}
}

type Duration struct {
	Hours   int64
	Minutes int64
	Seconds int64
}

func formatDuration(seconds int64) Duration {
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	remainingSeconds := seconds % 60

	return Duration{
		Hours:   hours,
		Minutes: minutes,
		Seconds: remainingSeconds,
	}
}

type ZipSummary struct {
	Path             string
	TotalFiles       int
	AlreadyExtracted int
	EstimatedTime    Duration
}

// FileInfo holds metadata about a file
type FileInfo struct {
	Size    int64
	ModTime time.Time
	Mode    os.FileMode
}

// GetFileInfo returns size, modification time and mode of a file
func GetFileInfo(path string) (*FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory")
	}
	return &FileInfo{
		Size:    info.Size(),
		ModTime: info.ModTime(),
		Mode:    info.Mode(),
	}, nil
}

// IsFileEqual checks if a file at destPath matches the expected zip file entry
func IsFileEqual(f *zip.File, destPath string) (bool, string) {
	destInfo, err := GetFileInfo(destPath)
	if err != nil {
		return false, fmt.Sprintf("error accessing file: %v", err)
	}

	// Always check size first
	if destInfo.Size != int64(f.UncompressedSize64) {
		return false, fmt.Sprintf("size mismatch: zip=%d, existing=%d", f.UncompressedSize64, destInfo.Size)
	}

	// Always check modification time
	timeDiff := destInfo.ModTime.Sub(f.Modified).Abs()
	if timeDiff > 2*time.Second {
		return false, fmt.Sprintf("time mismatch: zip=%v, existing=%v", f.Modified, destInfo.ModTime)
	}

	// For large files (>= hashThreshold), skip content comparison
	if int64(f.UncompressedSize64) >= hashThreshold {
		return true, ""
	}

	// For smaller files, also compare content hash
	equal, err := compareFileHash(f, destPath)
	if err != nil {
		return false, fmt.Sprintf("hash comparison error: %v", err)
	}
	if !equal {
		return false, "content mismatch (different hash)"
	}

	return true, ""
}

func compareFileHash(f *zip.File, destPath string) (bool, error) {
	h1 := sha256.New()
	h2 := sha256.New()

	// Hash zip file content
	rc, err := f.Open()
	if err != nil {
		return false, err
	}
	defer rc.Close()
	if _, err := io.Copy(h1, rc); err != nil {
		return false, err
	}

	// Hash existing file
	file, err := os.Open(destPath)
	if err != nil {
		return false, err
	}
	defer file.Close()
	if _, err := io.Copy(h2, file); err != nil {
		return false, err
	}

	return bytes.Equal(h1.Sum(nil), h2.Sum(nil)), nil
}

func FileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func (z *ZipExtractor) shouldIncludeFile(zipPath string) (string, bool) {
	if z.basePath == "" || z.basePath == "." {
		return zipPath, true
	}

	if !strings.HasPrefix(zipPath, z.basePath) {
		return "", false
	}

	relPath := strings.TrimPrefix(zipPath, z.basePath)
	relPath = strings.TrimPrefix(relPath, "/")
	return relPath, true
}

func (z *ZipExtractor) EstimateTime(zipPath string) (*ZipSummary, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("opening zip: %w", err)
	}
	defer r.Close()

	var totalSize int64
	var totalFiles, alreadyExtracted int

	for _, f := range r.File {
		relPath, include := z.shouldIncludeFile(f.Name)
		if !include {
			continue
		}

		totalFiles++
		destPath := filepath.Join(z.destFolder, relPath)
		if FileExists(destPath) {
			alreadyExtracted++
			continue
		}
		totalSize += int64(f.UncompressedSize64)
	}

	estimatedSeconds := totalSize / assumedExtractionSpeed
	return &ZipSummary{zipPath, totalFiles, alreadyExtracted, formatDuration(estimatedSeconds)}, nil
}

func (z *ZipExtractor) Unzip(zipPath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip: %w", err)
	}
	defer r.Close()

	fmt.Printf("\nProcessing ZIP: %s\n", zipPath)
	if z.basePath != "" && z.basePath != "." {
		fmt.Printf("Starting from path: %s\n", z.basePath)
	}

	if z.dryRun {
		fmt.Printf("DRY RUN - Would extract %d files\n", len(r.File))
		for _, f := range r.File {
			relPath, include := z.shouldIncludeFile(f.Name)
			if !include {
				continue
			}
			destPath := filepath.Join(z.destFolder, relPath)
			if f.FileInfo().IsDir() {
				continue
			}
			z.ExtractFile(f, destPath)
		}
		return nil
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, z.workers)

	var extractionErrors []error
	var errMutex sync.Mutex

	totalFiles := len(r.File)
	globalBar := progressbar.NewOptions(totalFiles,
		progressbar.OptionSetDescription("Overall Progress"),
		progressbar.OptionShowCount(),
		progressbar.OptionSetWidth(50),
		progressbar.OptionThrottle(65*time.Millisecond),
		progressbar.OptionClearOnFinish(),
	)

	for _, f := range r.File {
		relPath, include := z.shouldIncludeFile(f.Name)
		if !include {
			continue
		}

		destPath := filepath.Join(z.destFolder, relPath)
		if f.FileInfo().IsDir() {
			os.MkdirAll(destPath, os.ModePerm)
			continue
		}

		wg.Add(1)
		sem <- struct{}{}

		go func(f *zip.File, destPath string) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := z.ExtractFile(f, destPath); err != nil {
				errMutex.Lock()
				extractionErrors = append(extractionErrors, fmt.Errorf("error extracting %s: %w", destPath, err))
				errMutex.Unlock()
			}
			globalBar.Add(1)
		}(f, destPath)
	}

	wg.Wait()
	fmt.Println("\nFinished processing ZIP:", zipPath)

	if len(extractionErrors) > 0 {
		return fmt.Errorf("failed to extract some files: %v", extractionErrors[0])
	}
	return nil
}

func (z *ZipExtractor) logExtraction(path, destPath string, size int64, status, reason string) {
	z.logsMutex.Lock()
	defer z.logsMutex.Unlock()
	z.logs = append(z.logs, ExtractionLog{
		Path:      path,
		DestPath:  destPath,
		Size:      size,
		Status:    status,
		Reason:    reason,
		Timestamp: time.Now(),
		DryRun:    z.dryRun,
	})
}

func (z *ZipExtractor) GetLogs() []ExtractionLog {
	z.logsMutex.Lock()
	defer z.logsMutex.Unlock()
	// Return a copy to prevent external modifications
	logsCopy := make([]ExtractionLog, len(z.logs))
	copy(logsCopy, z.logs)
	return logsCopy
}

func (z *ZipExtractor) ExtractFile(f *zip.File, destPath string) error {
	if z.dryRun {
		equal, reason := IsFileEqual(f, destPath)
		if equal {
			z.logExtraction(f.Name, destPath, int64(f.UncompressedSize64), "Skipped", "File already exists and matches")
			return nil
		}
		var extractReason string
		if FileExists(destPath) {
			extractReason = fmt.Sprintf("File exists but %s", reason)
		} else {
			extractReason = "File does not exist"
		}
		z.logExtraction(f.Name, destPath, int64(f.UncompressedSize64), "Would Extract", extractReason)
		return nil
	}

	equal, reason := IsFileEqual(f, destPath)
	if equal {
		z.logExtraction(f.Name, destPath, int64(f.UncompressedSize64), "Skipped", "File already exists and matches")
		return nil
	}
	if FileExists(destPath) {
		z.logExtraction(f.Name, destPath, int64(f.UncompressedSize64), "Replacing", reason)
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := ExtractAndVerify(f, destPath)
		if err == nil {
			z.logExtraction(f.Name, destPath, int64(f.UncompressedSize64), "Extracted", "")
			return nil
		}
		if attempt < maxRetries {
			z.logExtraction(f.Name, destPath, int64(f.UncompressedSize64), "Retry",
				fmt.Sprintf("Attempt %d/%d failed: %v", attempt, maxRetries, err))
		} else {
			z.logExtraction(f.Name, destPath, int64(f.UncompressedSize64), "Failed",
				fmt.Sprintf("All %d attempts failed: %v", maxRetries, err))
		}
	}
	return fmt.Errorf("failed after %d attempts: %s", maxRetries, destPath)
}

func ExtractAndVerify(f *zip.File, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), os.ModePerm); err != nil {
		return err
	}

	srcFile, err := f.Open()
	if err != nil {
		return err
	}
	defer srcFile.Close()

	destFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, srcFile)
	if err != nil {
		return err
	}

	// Close the file before setting timestamps
	destFile.Close()

	// Preserve timestamps from the zip file
	modTime := f.Modified
	if err := os.Chtimes(destPath, modTime, modTime); err != nil {
		return fmt.Errorf("failed to set file times: %w", err)
	}

	return nil
}

func writeLogsToFile(logs []ExtractionLog, path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer f.Close()

	// Write header if file is empty
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat log file: %w", err)
	}
	if info.Size() == 0 {
		fmt.Fprintln(f, "Timestamp,Path,DestPath,Size,Status,Reason,DryRun")
	}

	// Write logs in CSV format
	for _, log := range logs {
		_, err := fmt.Fprintf(f, "%s,%s,%s,%d,%s,%q,%v\n",
			log.Timestamp.Format(time.RFC3339),
			log.Path,
			log.DestPath,
			log.Size,
			log.Status,
			log.Reason,
			log.DryRun)
		if err != nil {
			return fmt.Errorf("failed to write log: %w", err)
		}
	}
	return nil
}

func main() {
	flag.Parse()
	args := flag.Args()

	if len(args) < 2 {
		fmt.Println("Usage: unzip-takeout [flags] <destination_folder> <zip1> <zip2> ... <zipN>")
		fmt.Println("\nFlags must be specified before the destination folder and zip files.")
		fmt.Println("\nFlags:")
		fmt.Println("  --workers=N                 Number of parallel extraction workers (default: 4)")
		fmt.Println("  --auto                      Skip confirmation and auto-start extraction")
		fmt.Println("  --dry-run                   Show extraction details without performing extraction")
		fmt.Println("  --base-path=\"PATH\"          Base path within the ZIP file to start extraction from")
		fmt.Println("  --log=\"PATH\"                Path to write extraction logs")
		os.Exit(1)
	}

	destFolder := args[0]
	zipFiles := args[1:]

	if !dryRun {
		if err := os.MkdirAll(destFolder, os.ModePerm); err != nil {
			fmt.Println("Error creating destination folder:", err)
			os.Exit(1)
		}
	}

	if dryRun {
		fmt.Println("DRY RUN!")
	}

	extractor := NewZipExtractor(maxWorkers, autoMode, dryRun, destFolder, basePath)

	var confirmedZips []string
	var totalEstimatedTime int64
	var totalFilesToExtract int

	for _, zipFile := range zipFiles {
		summary, err := extractor.EstimateTime(zipFile)
		if err != nil {
			fmt.Println("Skipping ZIP due to error:", zipFile, err)
			continue
		}

		filesToExtract := summary.TotalFiles - summary.AlreadyExtracted
		fmt.Printf("\nZIP: %s\nTotal Files: %d\nAlready Extracted: %d\nFiles to Extract: %d\nEstimated Time: ~%dh %dm %ds\n",
			zipFile, summary.TotalFiles, summary.AlreadyExtracted, filesToExtract,
			summary.EstimatedTime.Hours, summary.EstimatedTime.Minutes, summary.EstimatedTime.Seconds)

		if filesToExtract == 0 {
			fmt.Println("✅ Everything already extracted. Skipping...")
			continue
		}

		if !autoMode {
			var choice string
			fmt.Print("Confirm extraction for this ZIP? (y/N): ")
			fmt.Scanln(&choice)
			if choice != "y" {
				fmt.Println("Skipping...")
				continue
			}
		}

		confirmedZips = append(confirmedZips, zipFile)
		totalEstimatedTime += int64(summary.EstimatedTime.Hours*3600 + summary.EstimatedTime.Minutes*60 + summary.EstimatedTime.Seconds)
		totalFilesToExtract += filesToExtract
	}

	if len(confirmedZips) == 0 {
		fmt.Println("\n✅ No extractions needed. Exiting.")
		return
	}

	fmt.Printf("\nFinal Extraction Summary:\nConfirmed ZIPs: %d\nTotal Files to Extract: %d\nTotal Estimated Time: ~%dh %dm %ds",
		len(confirmedZips), totalFilesToExtract,
		formatDuration(totalEstimatedTime).Hours,
		formatDuration(totalEstimatedTime).Minutes,
		formatDuration(totalEstimatedTime).Seconds)

	if !autoMode {
		var finalChoice string
		fmt.Print("\nProceed with extraction? (y/N): ")
		fmt.Scanln(&finalChoice)
		if finalChoice != "y" {
			fmt.Println("🚫 Extraction canceled.")
			return
		}
	}

	for _, zipFile := range confirmedZips {
		err := extractor.Unzip(zipFile)
		if err != nil {
			fmt.Printf("Error processing %s: %v\n", zipFile, err)
			continue
		}

		// Print extraction summary with dry run indicator
		if dryRun {
			fmt.Printf("\n🔍 DRY RUN - Extraction Log for %s:\n", zipFile)
		} else {
			fmt.Printf("\nExtraction Log for %s:\n", zipFile)
		}
		fmt.Println("----------------------------------------")
		for _, log := range extractor.GetLogs() {
			prefix := ""
			if log.DryRun {
				prefix = "[DRY RUN] "
			}

			switch log.Status {
			case "Extracted":
				fmt.Printf("%s✅ %s -> %s (%.2f MB)\n", prefix, log.Path, log.DestPath, float64(log.Size)/(1024*1024))
			case "Skipped":
				fmt.Printf("%s⏭️  %s: %s\n", prefix, log.Path, log.Reason)
			case "Failed":
				fmt.Printf("%s❌ %s: %s\n", prefix, log.Path, log.Reason)
			case "Would Extract":
				fmt.Printf("%s🔍 %s -> %s (%.2f MB)\n", prefix, log.Path, log.DestPath, float64(log.Size)/(1024*1024))
			}
		}
		fmt.Println("----------------------------------------")

		// Write logs to file if requested
		if logFile != "" {
			if err := writeLogsToFile(extractor.GetLogs(), logFile); err != nil {
				fmt.Printf("Warning: Failed to write logs to file: %v\n", err)
			}
		}
	}

	if dryRun {
		fmt.Println("\n🔍 DRY RUN completed - no files were modified.")
	} else {
		fmt.Println("\n✅ All confirmed ZIP files processed successfully.")
	}
}

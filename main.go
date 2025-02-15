package main

import (
	"archive/zip"
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

const maxRetries = 3
const assumedExtractionSpeed = 100 * 1024 * 1024 // 100MB/s extraction speed assumption

func init() {
	flag.IntVar(&maxWorkers, "workers", 4, "Number of parallel extraction workers")
	flag.BoolVar(&autoMode, "auto", false, "Skip confirmation and auto-start extraction")
	flag.BoolVar(&dryRun, "dry-run", false, "Show extraction details without performing extraction")
	flag.StringVar(&basePath, "base-path", "", "Base path within the ZIP file to start extraction from")
}

type ZipExtractor struct {
	workers    int
	autoMode   bool
	dryRun     bool
	destFolder string
	basePath   string
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
			if err := ExtractFile(f, destPath); err != nil {
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

func ExtractFile(f *zip.File, destPath string) error {
	if FileExists(destPath) {
		destInfo, _ := os.Stat(destPath)
		if destInfo.Size() == int64(f.UncompressedSize64) {
			return nil
		}
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := ExtractAndVerify(f, destPath)
		if err == nil {
			return nil
		}
		fmt.Printf("Retrying extraction (%d/%d) for: %s\n", attempt, maxRetries, destPath)
		time.Sleep(time.Second * 2)
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

func main() {
	flag.Parse()
	args := flag.Args()

	if len(args) < 2 {
		fmt.Println("Usage: takeout-to-icloud [flags] <destination_folder> <zip1> <zip2> ... <zipN>")
		fmt.Println("\nFlags must be specified before the destination folder and zip files.")
		fmt.Println("\nFlags:")
		fmt.Println("  --workers=N                 Number of parallel extraction workers (default: 4)")
		fmt.Println("  --auto                      Skip confirmation and auto-start extraction")
		fmt.Println("  --dry-run                   Show extraction details without performing extraction")
		fmt.Println("  --base-path=\"PATH\"          Base path within the ZIP file to start extraction from")
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
		extractor.Unzip(zipFile)
	}

	fmt.Println("\n✅ All confirmed ZIP files processed successfully.")
}

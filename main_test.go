package main

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type testFile struct {
	name    string
	content string
	isDir   bool
	modTime time.Time
	mode    os.FileMode
	size    int64
}

func createTestZip(t *testing.T, files []testFile) string {
	t.Helper()

	tmpZip, err := os.CreateTemp("", "test-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer tmpZip.Close()

	w := zip.NewWriter(tmpZip)
	defer w.Close()

	for _, file := range files {
		if file.isDir {
			_, err := w.Create(file.name + "/")
			if err != nil {
				t.Fatal(err)
			}
			continue
		}

		// Set default mode if not specified
		mode := file.mode
		if mode == 0 {
			mode = 0644
		}

		// Set default modTime if not specified
		modTime := file.modTime
		if modTime.IsZero() {
			modTime = time.Now()
		}

		fh := &zip.FileHeader{
			Name:     file.name,
			Method:   zip.Deflate,
			Modified: modTime,
		}
		fh.SetMode(mode)

		f, err := w.CreateHeader(fh)
		if err != nil {
			t.Fatal(err)
		}

		if file.size > 0 {
			// For large files, write in chunks
			pattern := []byte(file.content)
			remaining := file.size
			for remaining > 0 {
				writeSize := int64(len(pattern))
				if remaining < writeSize {
					writeSize = remaining
				}
				if _, err := f.Write(pattern[:writeSize]); err != nil {
					t.Fatal(err)
				}
				remaining -= writeSize
			}
		} else {
			// For normal files, write content directly
			_, err = f.Write([]byte(file.content))
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	return tmpZip.Name()
}

func setupTestEnvironment(t *testing.T) (string, string, func()) {
	tempDir, err := os.MkdirTemp("", "extract-test-*")
	if err != nil {
		t.Fatal(err)
	}

	// Set directory permissions
	if err := os.Chmod(tempDir, 0755); err != nil {
		t.Fatal(err)
	}

	testFiles := []testFile{
		{name: "test1.txt", content: "test file 1 content", mode: 0644},
		{name: "dir1", isDir: true, mode: 0755},
		{name: "dir1/test2.txt", content: "test file 2 content", mode: 0644},
		{name: "dir1/dir2", isDir: true, mode: 0755},
		{name: "dir1/dir2/test3.txt", content: "test file 3 content", mode: 0644},
	}

	zipPath := createTestZip(t, testFiles)

	return zipPath, tempDir, func() {
		os.Remove(zipPath)
		os.RemoveAll(tempDir)
	}
}

func TestUnzip(t *testing.T) {
	zipPath, extractDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	extractor := NewZipExtractor(4, true, false, extractDir, "")
	err := extractor.Unzip(zipPath)
	if err != nil {
		t.Errorf("Unzip failed: %v", err)
	}

	// Verify extracted files
	expectedFiles := []string{
		"test1.txt",
		filepath.Join("dir1", "test2.txt"),
		filepath.Join("dir1", "dir2", "test3.txt"),
	}

	for _, file := range expectedFiles {
		path := filepath.Join(extractDir, file)
		if !FileExists(path) {
			t.Errorf("Expected file not found: %s", path)
		}
	}
}

func TestFileExists(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) (string, func())
		want  bool
	}{
		{
			name: "existing file",
			setup: func(t *testing.T) (string, func()) {
				f, err := os.CreateTemp("", "test-*")
				if err != nil {
					t.Fatal(err)
				}
				return f.Name(), func() {
					f.Close()
					os.Remove(f.Name())
				}
			},
			want: true,
		},
		{
			name: "non-existing file",
			setup: func(t *testing.T) (string, func()) {
				return "non-existing-file.txt", func() {}
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, cleanup := tt.setup(t)
			defer cleanup()

			if got := FileExists(path); got != tt.want {
				t.Errorf("FileExists() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEstimateTime(t *testing.T) {
	zipPath, extractDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	extractor := NewZipExtractor(4, true, false, extractDir, "")
	summary, err := extractor.EstimateTime(zipPath)
	if err != nil {
		t.Errorf("EstimateTime failed: %v", err)
	}

	expectedFiles := 5 // 3 files + 2 directories
	if summary.TotalFiles != expectedFiles {
		t.Errorf("Expected %d total files (including directories), got %d", expectedFiles, summary.TotalFiles)
	}

	if summary.AlreadyExtracted != 0 {
		t.Errorf("Expected 0 already extracted files, got %d", summary.AlreadyExtracted)
	}
}

func TestCorruptZip(t *testing.T) {
	// Create a corrupt zip file
	testFiles := []testFile{
		{name: "corrupt.txt", content: "corrupted"},
	}

	zipPath := createTestZip(t, testFiles)
	defer os.Remove(zipPath)

	// Corrupt the zip file by writing random bytes
	f, err := os.OpenFile(zipPath, os.O_WRONLY, 0666)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("corrupt"), 0); err != nil {
		t.Fatal(err)
	}
	f.Close()

	extractDir, err := os.MkdirTemp("", "extract-corrupt-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(extractDir)

	extractor := NewZipExtractor(4, true, false, extractDir, "")
	err = extractor.Unzip(zipPath)
	if err != nil {
		// Expected behavior for corrupt zip
		return
	}
	t.Error("Expected error for corrupt zip file")
}

func TestDryRun(t *testing.T) {
	zipPath, extractDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Create extractor with dry run enabled
	extractor := NewZipExtractor(4, true, true, extractDir, "")

	// Test estimation
	summary, err := extractor.EstimateTime(zipPath)
	if err != nil {
		t.Errorf("EstimateTime failed in dry run: %v", err)
	}

	// Verify summary contains expected values
	if summary.TotalFiles != 5 { // 3 files + 2 directories
		t.Errorf("Expected 5 total files in dry run, got %d", summary.TotalFiles)
	}

	// Attempt extraction in dry run mode
	err = extractor.Unzip(zipPath)
	if err != nil {
		t.Errorf("Unzip failed in dry run: %v", err)
	}

	// Verify no files were actually extracted
	files, err := os.ReadDir(extractDir)
	if err != nil {
		t.Errorf("Failed to read extract directory: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("Expected no files to be extracted in dry run mode, found %d files", len(files))
	}
}

func TestDryRunWithExistingFiles(t *testing.T) {
	zipPath, extractDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Create a file that would conflict with extraction
	existingFilePath := filepath.Join(extractDir, "test1.txt")
	if err := os.MkdirAll(filepath.Dir(existingFilePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existingFilePath, []byte("existing content"), 0644); err != nil {
		t.Fatal(err)
	}

	extractor := NewZipExtractor(4, true, true, extractDir, "")

	summary, err := extractor.EstimateTime(zipPath)
	if err != nil {
		t.Errorf("EstimateTime failed in dry run: %v", err)
	}

	// Verify the existing file is counted correctly
	if summary.AlreadyExtracted != 1 {
		t.Errorf("Expected 1 already extracted file in dry run, got %d", summary.AlreadyExtracted)
	}

	// Verify file content wasn't changed
	content, err := os.ReadFile(existingFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "existing content" {
		t.Error("Existing file content was modified during dry run")
	}
}

func TestZipExtractor(t *testing.T) {
	tests := []struct {
		name     string
		files    []testFile
		dryRun   bool
		wantErr  bool
		validate func(*testing.T, string)
	}{
		{
			name: "basic extraction",
			files: []testFile{
				{name: "test1.txt", content: "test content 1", mode: 0644},
				{name: "dir1/", isDir: true, mode: 0755},
				{name: "dir1/test2.txt", content: "test content 2", mode: 0644},
			},
			wantErr: false,
			validate: func(t *testing.T, extractDir string) {
				expectedFiles := []struct {
					path    string
					content string
				}{
					{"test1.txt", "test content 1"},
					{"dir1/test2.txt", "test content 2"},
				}

				for _, ef := range expectedFiles {
					path := filepath.Join(extractDir, ef.path)
					content, err := os.ReadFile(path)
					if err != nil {
						t.Errorf("failed to read extracted file %s: %v", path, err)
						continue
					}
					if string(content) != ef.content {
						t.Errorf("file %s content = %q, want %q", path, content, ef.content)
					}
				}
			},
		},
		{
			name:    "empty zip",
			files:   []testFile{},
			wantErr: false,
		},
		{
			name: "dry run mode",
			files: []testFile{
				{name: "test1.txt", content: "test content 1", mode: 0644},
				{name: "dir1/test2.txt", content: "test content 2", mode: 0644},
			},
			dryRun:  true,
			wantErr: false,
			validate: func(t *testing.T, extractDir string) {
				// Verify no files were extracted
				files, err := os.ReadDir(extractDir)
				if err != nil {
					t.Errorf("Failed to read extract directory: %v", err)
				}
				if len(files) != 0 {
					t.Errorf("Expected no files in dry run mode, found %d files", len(files))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			extractDir, err := os.MkdirTemp("", "extract-test-*")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(extractDir)

			zipPath := createTestZip(t, tt.files)
			defer os.Remove(zipPath)

			// Create extractor
			extractor := NewZipExtractor(2, true, tt.dryRun, extractDir, "")

			// Test
			err = extractor.Unzip(zipPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unzip() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Validate
			if tt.validate != nil {
				tt.validate(t, extractDir)
			}
		})
	}
}

func TestZipExtractorWithBasePath(t *testing.T) {
	tests := []struct {
		name     string
		files    []testFile
		basePath string
		want     []struct {
			path    string
			content string
			exists  bool
		}
	}{
		{
			name: "extract from subfolder",
			files: []testFile{
				{name: "root.txt", content: "root content", mode: 0644},
				{name: "subfolder/", isDir: true, mode: 0755},
				{name: "subfolder/test1.txt", content: "test content 1", mode: 0644},
				{name: "subfolder/test2.txt", content: "test content 2", mode: 0644},
				{name: "other/test3.txt", content: "should not extract", mode: 0644},
			},
			basePath: "subfolder",
			want: []struct {
				path    string
				content string
				exists  bool
			}{
				{"test1.txt", "test content 1", true},
				{"test2.txt", "test content 2", true},
				{"../root.txt", "", false},
				{"../other/test3.txt", "", false},
			},
		},
		{
			name: "extract from nested path",
			files: []testFile{
				{name: "Takeout/Drive/MyFolder/", isDir: true, mode: 0755},
				{name: "Takeout/Drive/MyFolder/doc1.txt", content: "document 1", mode: 0644},
				{name: "Takeout/Drive/OtherFolder/doc2.txt", content: "document 2", mode: 0644},
			},
			basePath: "Takeout/Drive/MyFolder",
			want: []struct {
				path    string
				content string
				exists  bool
			}{
				{"doc1.txt", "document 1", true},
				{"../OtherFolder/doc2.txt", "", false},
			},
		},
		{
			name: "non-existent base path",
			files: []testFile{
				{name: "test.txt", content: "test content", mode: 0644},
			},
			basePath: "NonExistentPath",
			want: []struct {
				path    string
				content string
				exists  bool
			}{
				{"test.txt", "", false},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			extractDir, err := os.MkdirTemp("", "extract-test-*")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(extractDir)

			zipPath := createTestZip(t, tt.files)
			defer os.Remove(zipPath)

			// Create extractor with base path
			extractor := NewZipExtractor(2, true, false, extractDir, tt.basePath)

			// Test extraction
			err = extractor.Unzip(zipPath)
			if err != nil {
				t.Errorf("Unzip() error = %v", err)
				return
			}

			// Verify extracted files
			for _, w := range tt.want {
				path := filepath.Join(extractDir, w.path)
				exists := FileExists(path)
				if exists != w.exists {
					t.Errorf("file %s: exists = %v, want %v", w.path, exists, w.exists)
					continue
				}
				if w.exists {
					content, err := os.ReadFile(path)
					if err != nil {
						t.Errorf("failed to read file %s: %v", w.path, err)
						continue
					}
					if string(content) != w.content {
						t.Errorf("file %s: content = %q, want %q", w.path, content, w.content)
					}
				}
			}
		})
	}
}

func TestMetadataPreservation(t *testing.T) {
	// Setup test files with specific timestamps and permissions
	testTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	testFiles := []testFile{
		{
			name:    "test.txt",
			content: "test content",
			// File will be created with this specific time in the zip
			modTime: testTime,
			mode:    0644,
		},
	}

	// Create temporary directories
	extractDir, err := os.MkdirTemp("", "extract-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(extractDir)

	// Create zip file with metadata
	tmpZip, err := os.CreateTemp("", "test-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpZip.Name())

	w := zip.NewWriter(tmpZip)
	for _, file := range testFiles {
		fh := &zip.FileHeader{
			Name:     file.name,
			Method:   zip.Deflate,
			Modified: testTime,
		}
		fh.SetMode(file.mode)

		f, err := w.CreateHeader(fh)
		if err != nil {
			t.Fatal(err)
		}

		_, err = f.Write([]byte(file.content))
		if err != nil {
			t.Fatal(err)
		}
	}
	w.Close()

	// Extract and verify metadata
	extractor := NewZipExtractor(1, true, false, extractDir, "")
	err = extractor.Unzip(tmpZip.Name())
	if err != nil {
		t.Fatalf("Failed to extract: %v", err)
	}

	// Check extracted file
	extractedPath := filepath.Join(extractDir, "test.txt")
	info, err := os.Stat(extractedPath)
	if err != nil {
		t.Fatalf("Failed to stat extracted file: %v", err)
	}

	// Verify modification time
	if !info.ModTime().Equal(testTime) {
		t.Errorf("Modification time not preserved. Got %v, want %v",
			info.ModTime(), testTime)
	}

	// Verify permissions (masking out the file type bits)
	if info.Mode().Perm() != 0644 {
		t.Errorf("File permissions not preserved. Got %v, want %v",
			info.Mode().Perm(), 0644)
	}

	// Verify content
	content, err := os.ReadFile(extractedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "test content" {
		t.Errorf("File content not preserved. Got %q, want %q",
			string(content), "test content")
	}
}

func TestIsFileEqual(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fileequal-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	testTime := time.Now().Round(time.Second)
	content := "test content"

	tests := []struct {
		name       string
		setupFn    func(t *testing.T) (*zip.File, string, func())
		wantEqual  bool
		wantReason string
	}{
		{
			name: "identical files",
			setupFn: func(t *testing.T) (*zip.File, string, func()) {
				zipFile := createTestZip(t, []testFile{
					{
						name:    "test.txt",
						content: content,
						modTime: testTime,
						mode:    0644,
					},
				})

				destPath := filepath.Join(tmpDir, "test.txt")
				err := os.WriteFile(destPath, []byte(content), 0644)
				if err != nil {
					t.Fatal(err)
				}
				err = os.Chtimes(destPath, testTime, testTime)
				if err != nil {
					t.Fatal(err)
				}

				r, err := zip.OpenReader(zipFile)
				if err != nil {
					t.Fatal(err)
				}

				return r.File[0], destPath, func() {
					r.Close()
					os.Remove(zipFile)
				}
			},
			wantEqual:  true,
			wantReason: "",
		},
		{
			name: "different content same size",
			setupFn: func(t *testing.T) (*zip.File, string, func()) {
				// Use strings of same length
				zipContent := "test content"
				fileContent := "different!!!" // Same length as "test content"

				zipFile := createTestZip(t, []testFile{
					{
						name:    "test.txt",
						content: zipContent,
						modTime: testTime,
						mode:    0644,
					},
				})

				destPath := filepath.Join(tmpDir, "test.txt")
				err := os.WriteFile(destPath, []byte(fileContent), 0644)
				if err != nil {
					t.Fatal(err)
				}
				err = os.Chtimes(destPath, testTime, testTime)
				if err != nil {
					t.Fatal(err)
				}

				r, err := zip.OpenReader(zipFile)
				if err != nil {
					t.Fatal(err)
				}

				return r.File[0], destPath, func() {
					r.Close()
					os.Remove(zipFile)
				}
			},
			wantEqual:  false,
			wantReason: "content mismatch (different hash)",
		},
		{
			name: "different modification time",
			setupFn: func(t *testing.T) (*zip.File, string, func()) {
				zipFile := createTestZip(t, []testFile{
					{
						name:    "test.txt",
						content: content,
						modTime: testTime,
						mode:    0644,
					},
				})

				destPath := filepath.Join(tmpDir, "test.txt")
				err := os.WriteFile(destPath, []byte(content), 0644)
				if err != nil {
					t.Fatal(err)
				}
				differentTime := testTime.Add(time.Hour)
				err = os.Chtimes(destPath, differentTime, differentTime)
				if err != nil {
					t.Fatal(err)
				}

				r, err := zip.OpenReader(zipFile)
				if err != nil {
					t.Fatal(err)
				}

				return r.File[0], destPath, func() {
					r.Close()
					os.Remove(zipFile)
				}
			},
			wantEqual:  false,
			wantReason: "time mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			zipFile, destPath, cleanup := tt.setupFn(t)
			defer cleanup()

			gotEqual, gotReason := IsFileEqual(zipFile, destPath)
			if gotEqual != tt.wantEqual {
				t.Errorf("IsFileEqual() equal = %v, want %v", gotEqual, tt.wantEqual)
			}
			if tt.wantReason != "" && !strings.Contains(gotReason, tt.wantReason) {
				t.Errorf("IsFileEqual() reason = %q, want to contain %q", gotReason, tt.wantReason)
			}
		})
	}
}

func TestCompareFileHash(t *testing.T) {
	// Create a temporary directory for test files
	tmpDir, err := os.MkdirTemp("", "hash-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Helper function to create a zip file with specific content
	createZipWithContent := func(content string) (*zip.File, error) {
		zipPath := filepath.Join(tmpDir, "test.zip")
		file, err := os.Create(zipPath)
		if err != nil {
			return nil, err
		}
		defer file.Close()

		w := zip.NewWriter(file)
		zf, err := w.Create("test.txt")
		if err != nil {
			return nil, err
		}
		_, err = zf.Write([]byte(content))
		if err != nil {
			return nil, err
		}
		w.Close()

		r, err := zip.OpenReader(zipPath)
		if err != nil {
			return nil, err
		}
		return r.File[0], nil
	}

	tests := []struct {
		name        string
		zipContent  string
		fileContent string
		wantEqual   bool
		wantErr     bool
	}{
		{
			name:        "identical content",
			zipContent:  "test content",
			fileContent: "test content",
			wantEqual:   true,
			wantErr:     false,
		},
		{
			name:        "different content same size",
			zipContent:  "test content",
			fileContent: "different!!!",
			wantEqual:   false,
			wantErr:     false,
		},
		{
			name:        "empty files",
			zipContent:  "",
			fileContent: "",
			wantEqual:   true,
			wantErr:     false,
		},
		{
			name:        "large identical content",
			zipContent:  strings.Repeat("a", 1024*1024), // 1MB
			fileContent: strings.Repeat("a", 1024*1024),
			wantEqual:   true,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create zip file
			zipFile, err := createZipWithContent(tt.zipContent)
			if err != nil {
				t.Fatal(err)
			}

			// Create destination file
			destPath := filepath.Join(tmpDir, "dest.txt")
			err = os.WriteFile(destPath, []byte(tt.fileContent), 0644)
			if err != nil {
				t.Fatal(err)
			}

			// Test hash comparison
			equal, err := compareFileHash(zipFile, destPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("compareFileHash() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if equal != tt.wantEqual {
				t.Errorf("compareFileHash() = %v, want %v", equal, tt.wantEqual)
			}
		})
	}
}

func TestIsFileEqualWithSize(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fileequal-size-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	testTime := time.Now().Round(time.Second)

	tests := []struct {
		name     string
		fileSize int
		setup    func(t *testing.T, size int) (*zip.File, string)
		want     bool
	}{
		{
			name:     "small file with hash check",
			fileSize: 1024, // 1KB
			setup: func(t *testing.T, size int) (*zip.File, string) {
				content := strings.Repeat("a", size)
				testFiles := []testFile{
					{
						name:    "test.txt",
						content: content,
						modTime: testTime,
						mode:    0644,
					},
				}

				zipPath := createTestZip(t, testFiles)
				destPath := filepath.Join(tmpDir, "test.txt")
				err := os.WriteFile(destPath, []byte(content), 0644)
				if err != nil {
					t.Fatal(err)
				}
				err = os.Chtimes(destPath, testTime, testTime)
				if err != nil {
					t.Fatal(err)
				}

				r, err := zip.OpenReader(zipPath)
				if err != nil {
					t.Fatal(err)
				}

				return r.File[0], destPath
			},
			want: true,
		},
		{
			name:     "large file skips hash check",
			fileSize: 15 * 1024 * 1024, // 15MB (above threshold)
			setup: func(t *testing.T, size int) (*zip.File, string) {
				content := strings.Repeat("a", size)
				testFiles := []testFile{
					{
						name:    "test.txt",
						content: content,
						modTime: testTime,
						mode:    0644,
					},
				}

				zipPath := createTestZip(t, testFiles)
				destPath := filepath.Join(tmpDir, "test.txt")

				// Write slightly different content but same size
				differentContent := strings.Repeat("b", size)
				err := os.WriteFile(destPath, []byte(differentContent), 0644)
				if err != nil {
					t.Fatal(err)
				}
				err = os.Chtimes(destPath, testTime, testTime)
				if err != nil {
					t.Fatal(err)
				}

				r, err := zip.OpenReader(zipPath)
				if err != nil {
					t.Fatal(err)
				}

				return r.File[0], destPath
			},
			want: true, // Should return true because hash check is skipped
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			zipFile, destPath := tt.setup(t, tt.fileSize)
			equal, _ := IsFileEqual(zipFile, destPath) // Add _, to ignore reason
			if equal != tt.want {
				t.Errorf("IsFileEqual() = %v, want %v", equal, tt.want)
			}
		})
	}
}

func TestLargeFileHandling(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "large-file-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	testTime := time.Now().Round(time.Second)

	tests := []struct {
		name     string
		size     int64
		modifyFn func(string) error // function to modify the file
		want     bool               // expected IsFileEqual result
	}{
		{
			name: "just under threshold",
			size: hashThreshold - 1024, // 1KB under threshold
			modifyFn: func(path string) error {
				// Modify content but keep same size
				return modifyFileContent(path, "modified")
			},
			want: false, // Should detect difference via hash
		},
		{
			name: "just over threshold",
			size: hashThreshold + 1024, // 1KB over threshold
			modifyFn: func(path string) error {
				// Modify content AND timestamp
				if err := modifyFileContent(path, "modified"); err != nil {
					return err
				}
				newTime := time.Now().Add(time.Hour)
				return os.Chtimes(path, newTime, newTime)
			},
			want: false, // Should detect difference via timestamp
		},
		{
			name: "large file different size",
			size: 200 * 1024 * 1024, // 200MB
			modifyFn: func(path string) error {
				// Truncate file to change size
				return os.Truncate(path, 100*1024*1024)
			},
			want: false, // Should detect difference via size
		},
		{
			name:     "large file same attributes",
			size:     200 * 1024 * 1024, // 200MB
			modifyFn: nil,               // Don't modify the file
			want:     true,              // Should consider equal
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a pattern that's not too memory intensive
			pattern := []byte("Large file content simulation - ")

			// Create test file with specified size
			testFiles := []testFile{
				{
					name:    "large.bin",
					content: string(pattern), // Will be repeated
					modTime: testTime,
					mode:    0644,
					size:    tt.size,
				},
			}

			zipPath := createTestZip(t, testFiles)
			defer os.Remove(zipPath)

			// Extract the file
			extractDir := filepath.Join(tmpDir, tt.name)
			if err := os.MkdirAll(extractDir, 0755); err != nil {
				t.Fatal(err)
			}

			extractor := NewZipExtractor(1, true, false, extractDir, "")
			if err := extractor.Unzip(zipPath); err != nil {
				t.Fatal(err)
			}

			// Verify the extracted file
			extractedPath := filepath.Join(extractDir, "large.bin")
			info, err := os.Stat(extractedPath)
			if err != nil {
				t.Fatal(err)
			}

			if info.Size() != tt.size {
				t.Errorf("Expected file size %d, got %d", tt.size, info.Size())
			}

			// Test IsFileEqual behavior
			if tt.modifyFn != nil {
				err := tt.modifyFn(extractedPath)
				if err != nil {
					t.Fatalf("Failed to modify file: %v", err)
				}
			}

			// Re-open zip to test IsFileEqual
			r, err := zip.OpenReader(zipPath)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()

			equal, _ := IsFileEqual(r.File[0], extractedPath) // Add _, to ignore reason
			if equal != tt.want {
				t.Errorf("IsFileEqual() = %v, want %v", equal, tt.want)
			}
		})
	}
}

func modifyFileContent(path string, content string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteAt([]byte(content), 0)
	return err
}

func TestExtractionLogging(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "log-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	testTime := time.Now().Round(time.Second)
	tests := []struct {
		name     string
		files    []testFile
		dryRun   bool
		setup    func(string) // Function to setup pre-existing files
		wantLogs []struct {
			path   string
			status string
			reason string
		}
	}{
		{
			name: "normal extraction",
			files: []testFile{
				{name: "test1.txt", content: "content1", modTime: testTime},
				{name: "test2.txt", content: "content2", modTime: testTime},
			},
			dryRun: false,
			wantLogs: []struct {
				path   string
				status string
				reason string
			}{
				{"test1.txt", "Extracted", ""},
				{"test2.txt", "Extracted", ""},
			},
		},
		{
			name: "dry run mode",
			files: []testFile{
				{name: "test1.txt", content: "content1", modTime: testTime},
				{name: "test2.txt", content: "content2", modTime: testTime},
			},
			dryRun: true,
			wantLogs: []struct {
				path   string
				status string
				reason string
			}{
				{"test1.txt", "Would Extract", "File does not exist"},
				{"test2.txt", "Would Extract", "File does not exist"},
			},
		},
		{
			name: "skipping existing files",
			files: []testFile{
				{name: "test1.txt", content: "content1", modTime: testTime},
				{name: "test2.txt", content: "content2", modTime: testTime},
			},
			setup: func(dir string) {
				// Create pre-existing file
				path := filepath.Join(dir, "test1.txt")
				os.WriteFile(path, []byte("content1"), 0644)
				os.Chtimes(path, testTime, testTime)
			},
			wantLogs: []struct {
				path   string
				status string
				reason string
			}{
				{"test1.txt", "Skipped", "File already exists and matches"},
				{"test2.txt", "Extracted", ""},
			},
		},
		{
			name: "failed extraction simulation",
			files: []testFile{
				{name: "test1.txt", content: strings.Repeat("a", 1024*1024), modTime: testTime}, // 1MB file
			},
			setup: func(dir string) {
				// Make destination directory read-only to force failure
				os.Chmod(dir, 0555)
			},
			wantLogs: []struct {
				path   string
				status string
				reason string
			}{
				{"test1.txt", "Retry", "Attempt 1/3 failed: "},
				{"test1.txt", "Retry", "Attempt 2/3 failed: "},
				{"test1.txt", "Failed", "All 3 attempts failed: "},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test directory
			extractDir := filepath.Join(tmpDir, tt.name)
			if err := os.MkdirAll(extractDir, 0755); err != nil {
				t.Fatal(err)
			}

			// Run setup if provided
			if tt.setup != nil {
				tt.setup(extractDir)
			}

			// Create zip file
			zipPath := createTestZip(t, tt.files)
			defer os.Remove(zipPath)

			// Create extractor
			extractor := NewZipExtractor(1, true, tt.dryRun, extractDir, "")

			// Perform extraction
			extractor.Unzip(zipPath)

			// Verify logs
			logs := extractor.GetLogs()
			if len(logs) != len(tt.wantLogs) {
				t.Errorf("Got %d logs, want %d", len(logs), len(tt.wantLogs))
			}

			for i, wantLog := range tt.wantLogs {
				if i >= len(logs) {
					break
				}
				gotLog := logs[i]
				if gotLog.Path != wantLog.path {
					t.Errorf("Log[%d] path = %q, want %q", i, gotLog.Path, wantLog.path)
				}
				if gotLog.Status != wantLog.status {
					t.Errorf("Log[%d] status = %q, want %q", i, gotLog.Status, wantLog.status)
				}
				if wantLog.reason != "" && !strings.Contains(gotLog.Reason, wantLog.reason) {
					t.Errorf("Log[%d] reason = %q, should contain %q", i, gotLog.Reason, wantLog.reason)
				}
				if gotLog.Timestamp.IsZero() {
					t.Errorf("Log[%d] timestamp should not be zero", i)
				}
			}

			// Reset directory permissions if needed
			if tt.setup != nil {
				os.Chmod(extractDir, 0755)
			}
		})
	}
}

func TestLogRetention(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "log-retention-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test files
	testFiles := []testFile{
		{name: "test1.txt", content: "content1"},
		{name: "test2.txt", content: "content2"},
	}

	// Create multiple zip files
	zip1 := createTestZip(t, testFiles)
	zip2 := createTestZip(t, testFiles)
	defer os.Remove(zip1)
	defer os.Remove(zip2)

	extractor := NewZipExtractor(1, true, false, tmpDir, "")

	// Process first zip
	err = extractor.Unzip(zip1)
	if err != nil {
		t.Fatal(err)
	}

	firstZipLogs := len(extractor.GetLogs())
	if firstZipLogs != 2 {
		t.Errorf("Expected 2 logs from first zip, got %d", firstZipLogs)
	}

	// Process second zip
	err = extractor.Unzip(zip2)
	if err != nil {
		t.Fatal(err)
	}

	// Verify logs are accumulated
	totalLogs := len(extractor.GetLogs())
	if totalLogs != 4 { // 2 files * 2 zips
		t.Errorf("Expected 4 total logs, got %d", totalLogs)
	}
}

func TestLogFileWriting(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "log-file-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	logPath := filepath.Join(tmpDir, "extraction.log")
	testTime := time.Now().Round(time.Second)

	logs := []ExtractionLog{
		{
			Path:      "test1.txt",
			DestPath:  "/dest/test1.txt",
			Size:      1024,
			Status:    "Extracted",
			Reason:    "",
			Timestamp: testTime,
			DryRun:    false,
		},
		{
			Path:      "test2.txt",
			DestPath:  "/dest/test2.txt",
			Size:      2048,
			Status:    "Would Extract",
			Reason:    "Dry run mode",
			Timestamp: testTime,
			DryRun:    true,
		},
	}

	// Write logs
	if err := writeLogsToFile(logs, logPath); err != nil {
		t.Fatalf("Failed to write logs: %v", err)
	}

	// Read and verify log file
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	// Verify header
	lines := strings.Split(string(content), "\n")
	if len(lines) < 3 { // Header + 2 logs + empty line
		t.Fatalf("Expected at least 3 lines, got %d", len(lines))
	}
	if lines[0] != "Timestamp,Path,DestPath,Size,Status,Reason,DryRun" {
		t.Errorf("Unexpected header: %s", lines[0])
	}

	// Verify log entries
	expectedFirstLog := fmt.Sprintf("%s,test1.txt,/dest/test1.txt,1024,Extracted,\"\",false",
		testTime.Format(time.RFC3339))
	if strings.TrimSpace(lines[1]) != expectedFirstLog {
		t.Errorf("Unexpected log entry:\ngot:  %s\nwant: %s", lines[1], expectedFirstLog)
	}

	// Test appending
	if err := writeLogsToFile(logs[:1], logPath); err != nil {
		t.Fatalf("Failed to append logs: %v", err)
	}

	// Verify appended content
	content, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read updated log file: %v", err)
	}
	lines = strings.Split(string(content), "\n")
	if len(lines) < 4 { // Header + 2 original logs + 1 appended log + empty line
		t.Fatalf("Expected at least 4 lines after append, got %d", len(lines))
	}
}

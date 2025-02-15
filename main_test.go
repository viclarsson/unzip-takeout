package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testFile struct {
	name    string
	content string
	isDir   bool
	modTime time.Time
	mode    os.FileMode
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

		_, err = f.Write([]byte(file.content))
		if err != nil {
			t.Fatal(err)
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

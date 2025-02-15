# Unzip Takeout

A tool to efficiently extract Google Takeout archives to any destination, such as iCloud Drive, with smart file comparison to avoid re-extracting unchanged files.

As Google Takeout only exports files owned by the user, a shared Drive folder will need to be exported by all users contributing to the folder and therefore needs an efficient way to merge the files into a single folder.

Made for personal use, but could be useful for others, due to the changes in the previously free Google Workspaces where additional storage could be bought. We wanted to migrate files to iCloud and needed a simple way to export all users' files and merge them efficiently.

## Features

- Parallel extraction for faster processing
- Smart comparison to skip unchanged files
- Preserves file metadata (timestamps, permissions)
- Extract from specific paths within ZIP files
- Progress tracking and time estimation
- Detailed extraction logs

## Installation

```
go install github.com/viclarsson/unzip-takeout@latest
```

## Basic Usage

Extract a Takeout archive:

```
unzip-takeout ~/iCloud/Photos takeout.zip
```

Preview what would be extracted (dry run):

```
unzip-takeout --dry-run ~/iCloud/Photos takeout.zip
```

Extract only form the Google Photos folder:

```
unzip-takeout --base-path="Takeout/Google Photos" ~/iCloud/Photos takeout.zip
```

## Advanced Options

```
unzip-takeout [flags] <destination_folder> <zip1> <zip2> ... <zipN>

Flags:
  --workers=N        Number of parallel workers (default: 4)
  --auto            Skip confirmation prompts
  --dry-run         Preview without extracting
  --base-path=PATH  Extract from specific path in ZIP
  --log=PATH        Write operations to log file
```

## Examples

Extract multiple archives:

```
unzip-takeout ~/iCloud/Photos takeout-1.zip takeout-2.zip
```

Use 8 workers and log operations:

```
unzip-takeout --workers=8 --log=extraction.log ~/iCloud/Photos takeout.zip
```

Extract Drive files only:

```
unzip-takeout --base-path="Takeout/Drive" ~/iCloud/Drive takeout.zip
```

Extract from a specific Drive folder:

```
unzip-takeout --base-path="Takeout/Drive/Documents" ~/iCloud/Documents takeout.zip
```

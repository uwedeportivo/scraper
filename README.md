![scraper Hero](hero.png)

# scraper

**scraper** is a fast, concurrent Go CLI for downloading images from web pages. It uses a priority queue scheduler and a configurable worker pool to fetch assets efficiently, with automatic retry on transient errors.

## Features

- **Concurrent Downloads**: Spawns 20 parallel workers backed by a heap-based priority queue that deprioritizes recently-failed links.
- **Recursive Crawling**: Optionally follows anchor links within the same domain to discover and download images across an entire site.
- **Retry Logic**: Failed fetches are re-queued with backoff up to 3 attempts before giving up.
- **Dry Run Mode**: Preview all discovered URLs without writing any files to disk.
- **Unique Filenames**: Appends a UUID to each downloaded file to prevent collisions.

## Quick Start

```bash
# Download all images from a page
scraper --output ./images https://example.com

# Recursively crawl the entire site
scraper --recurse --output ./images https://example.com

# Dry run — print discovered links without downloading
scraper --dryrun https://example.com
```

## Options

| Flag        | Description                                      | Default |
|-------------|--------------------------------------------------|---------|
| `--output`  | Output directory for downloaded files (required) | `""`    |
| `--recurse` | Follow anchor links within the same domain       | `false` |
| `--dryrun`  | Print links without downloading                  | `false` |

## Build

### With Bazel (recommended)

```bash
bazel build //:scraper
bazel run //:scraper -- --dryrun https://example.com
```

### With Go

```bash
go build -o scraper .
./scraper --output ./images https://example.com
```

### Update Bazel deps after changing go.mod

```bash
bazel run //:gazelle
```

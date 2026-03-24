package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var (
	recurse bool
	dryrun  bool
	output  string
)

var diagnoseCmd = &cobra.Command{
	Use:   "diagnose url",
	Short: "Diagnose image scraping for a URL (shows what the page actually returns)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		u, err := url.Parse(args[0])
		if err != nil || !u.IsAbs() {
			return fmt.Errorf("please supply a valid absolute url")
		}
		resp, err := httpGet(args[0])
		if err != nil {
			return fmt.Errorf("fetch failed: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read body failed: %w", err)
		}

		fmt.Printf("Status:       %s\n", resp.Status)
		fmt.Printf("Content-Type: %s\n", resp.Header.Get("Content-Type"))
		fmt.Printf("Body size:    %d bytes\n\n", len(body))

		matches := redfinImageRe.FindAllString(string(body), -1)
		fmt.Printf("Regex matches (current pattern): %d\n", len(matches))
		for _, m := range matches {
			fmt.Printf("  %s\n", m)
		}

		broaderRe := regexp.MustCompile(`https?://[^\s"'\\]*cdn-redfin[^\s"'\\]*`)
		broader := broaderRe.FindAllString(string(body), -1)
		fmt.Printf("\nBroader cdn-redfin URLs found: %d\n", len(broader))
		seen := make(map[string]struct{})
		for _, m := range broader {
			if _, ok := seen[m]; ok {
				continue
			}
			seen[m] = struct{}{}
			fmt.Printf("  %s\n", m)
		}

		preview := string(body)
		if len(preview) > 3000 {
			preview = preview[:3000]
		}
		fmt.Printf("\n--- Body preview (first 3000 chars) ---\n%s\n", preview)
		return nil
	},
}

var rootCmd = &cobra.Command{
	Use:   "scraper [flags] url",
	Short: "A concurrent web image scraper",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mainUrl, err := url.Parse(args[0])
		if err != nil || !mainUrl.IsAbs() {
			return fmt.Errorf("please supply a valid absolute url")
		}
		if output != "" {
			if err := os.MkdirAll(output, 0755); err != nil {
				return fmt.Errorf("failed to create output directory: %w", err)
			}
		}
		p := tea.NewProgram(newModel())
		go run(mainUrl, p)
		_, err = p.Run()
		return err
	},
}

func init() {
	rootCmd.Flags().BoolVar(&recurse, "recurse", false, "recurse into linked pages with same domain")
	rootCmd.Flags().BoolVar(&dryrun, "dryrun", false, "dry run")
	rootCmd.Flags().StringVar(&output, "output", "", "output directory (created if it doesn't exist)")
	rootCmd.AddCommand(diagnoseCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

package main

import (
	"fmt"
	"net/url"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var (
	recurse bool
	dryrun  bool
	output  string
)

var rootCmd = &cobra.Command{
	Use:   "scraper [flags] url",
	Short: "A concurrent web image scraper",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mainUrl, err := url.Parse(args[0])
		if err != nil || !mainUrl.IsAbs() {
			return fmt.Errorf("please supply a valid absolute url")
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
	rootCmd.Flags().StringVar(&output, "output", "", "output directory (has to exist)")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

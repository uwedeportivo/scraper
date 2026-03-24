package main

import (
	"container/heap"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	"github.com/yhat/scrape"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	numWorkers = 20
	maxTries   = 3
	DEBUG      = false
)

var (
	redfinImageRe = regexp.MustCompile(`https://ssl\.cdn-redfin\.com/photo/\d+/bigphoto/\d+/[A-Za-z0-9_.]+\.jpg`)
	httpClient    = &http.Client{Timeout: 30 * time.Second}
)

func httpGet(rawURL string) (*http.Response, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	return httpClient.Do(req)
}

func isRedfinListing(u *url.URL) bool {
	return u.Host == "www.redfin.com" && strings.Contains(u.Path, "/home/")
}

type Link struct {
	url       *url.URL
	errs      []error
	lastFetch time.Time
	isLeaf    bool
	index     int
}

type LinkQueue []*Link

func (lq LinkQueue) Len() int { return len(lq) }

func (lq LinkQueue) Less(i, j int) bool {
	return lq[i].lastFetch.Before(lq[j].lastFetch) && len(lq[i].errs) < len(lq[j].errs)
}

func (lq LinkQueue) Swap(i, j int) {
	lq[i], lq[j] = lq[j], lq[i]
	lq[i].index = i
	lq[j].index = j
}

func (lq *LinkQueue) Push(x any) {
	n := len(*lq)
	item := x.(*Link)
	item.index = n
	*lq = append(*lq, item)
}

func (lq *LinkQueue) Pop() any {
	old := *lq
	n := len(old)
	item := old[n-1]
	item.index = -1
	*lq = old[0 : n-1]
	return item
}

type scheduler struct {
	wg   *sync.WaitGroup
	seen map[string]struct{}
	lq   LinkQueue
	wc   chan *Link
	ec   chan *Link
	sc   chan *Link
	pc   chan *Link
	prog *tea.Program
}

func (sch *scheduler) run() {
	numInflight := 0

	for {
		select {
		case lk := <-sch.ec:
			if len(lk.errs) < maxTries {
				heap.Push(&sch.lq, lk)
			} else {
				sch.prog.Send(msgError{url: lk.url.String()})
			}
		case lk := <-sch.sc:
			if _, seen := sch.seen[lk.url.String()]; !seen {
				heap.Push(&sch.lq, lk)
				sch.seen[lk.url.String()] = struct{}{}
			}
		case <-sch.pc:
			numInflight--
		default:
		}

		for numInflight < numWorkers && sch.lq.Len() > 0 {
			lk := heap.Pop(&sch.lq).(*Link)
			sch.wc <- lk
			numInflight++
		}

		if numInflight == 0 {
			close(sch.wc)
			sch.wg.Done()
			return
		}
	}
}

type worker struct {
	wg          *sync.WaitGroup
	wc          chan *Link
	ec          chan *Link
	sc          chan *Link
	pc          chan *Link
	mainUrl     *url.URL
	mainBaseUrl *url.URL
	prog        *tea.Program
}

func (w *worker) down(lk *Link) error {
	if DEBUG {
		fmt.Printf("down(%s)\n", lk.url.String())
		return nil
	}
	fid := uuid.NewString()
	filename := path.Base(lk.url.Path)
	if filename == "" {
		return fmt.Errorf("failed to derive file name from %v", lk.url)
	}
	ext := path.Ext(filename)
	filename = fmt.Sprintf("%s-%s%s", strings.TrimSuffix(filename, ext), fid, ext)

	out, err := os.Create(filepath.Join(output, filename))
	if err != nil {
		return err
	}
	defer out.Close()

	resp, err := httpGet(lk.url.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}
	w.prog.Send(msgImageDownloaded{filename: filename})
	return nil
}

func (w *worker) extractLink(n *html.Node) (*Link, error) {
	var urlStr string
	isLeaf := false

	switch n.DataAtom {
	case atom.A:
		if !recurse {
			return nil, nil
		}
		urlStr = scrape.Attr(n, "href")
	case atom.Img:
		urlStr = scrape.Attr(n, "src")
		if urlStr == "" {
			urlStr = scrape.Attr(n, "data-src")
		}
		isLeaf = true
	case atom.Frame:
		urlStr = scrape.Attr(n, "src")
	default:
		return nil, nil
	}
	if strings.HasPrefix(urlStr, "/") {
		urlStr = w.mainBaseUrl.String() + urlStr
	}

	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, err
	}

	if !isLeaf && u.IsAbs() && u.Host != w.mainUrl.Host {
		return nil, nil
	}

	if !u.IsAbs() {
		p := u.Path
		nu, _ := url.Parse(w.mainUrl.String())
		u = nu
		u.Path = path.Join(w.mainUrl.Path, p)
	}

	return &Link{url: u, isLeaf: isLeaf}, nil
}

func (w *worker) scrapeRedfin(lk *Link) error {
	resp, err := httpGet(lk.url.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	matches := redfinImageRe.FindAllString(string(body), -1)
	seen := make(map[string]struct{})
	found := 0
	for _, m := range matches {
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		u, err := url.Parse(m)
		if err != nil {
			continue
		}
		w.sc <- &Link{url: u, isLeaf: true}
		found++
	}
	w.prog.Send(msgPageScraped{url: lk.url.String(), links: found})
	return nil
}

func (w *worker) scrape(lk *Link) error {
	if isRedfinListing(lk.url) {
		return w.scrapeRedfin(lk)
	}

	resp, err := httpGet(lk.url.String())
	if err != nil {
		return err
	}

	root, err := html.Parse(resp.Body)
	if err != nil {
		return err
	}

	matcher := func(n *html.Node) bool {
		return n.DataAtom == atom.A || n.DataAtom == atom.Img || n.DataAtom == atom.Frame
	}

	nodes := scrape.FindAll(root, matcher)
	found := 0
	for _, n := range nodes {
		link, err := w.extractLink(n)
		if err != nil {
			return err
		}
		if link != nil {
			w.sc <- link
			found++
		}
	}
	w.prog.Send(msgPageScraped{url: lk.url.String(), links: found})
	return nil
}

func (w *worker) process(lk *Link) error {
	if lk.isLeaf {
		if dryrun {
			w.prog.Send(msgImageDownloaded{filename: lk.url.String()})
			return nil
		}
		return w.down(lk)
	}
	return w.scrape(lk)
}

func (w *worker) run() {
	for lk := range w.wc {
		err := w.process(lk)
		if err != nil {
			lk.errs = append(lk.errs, err)
			lk.lastFetch = time.Now()
			w.ec <- lk
		}
		w.pc <- lk
	}
	w.wg.Done()
}

func run(mainUrl *url.URL, prog *tea.Program) {
	mainBaseUrl := *mainUrl
	mainBaseUrl.Path = ""
	mainBaseUrl.RawQuery = ""
	mainBaseUrl.Fragment = ""

	var wg sync.WaitGroup

	wc := make(chan *Link, numWorkers)
	ec := make(chan *Link)
	sc := make(chan *Link)
	pc := make(chan *Link)

	wg.Add(numWorkers + 1)

	for range numWorkers {
		w := &worker{
			wc:          wc,
			ec:          ec,
			sc:          sc,
			pc:          pc,
			wg:          &wg,
			mainUrl:     mainUrl,
			mainBaseUrl: &mainBaseUrl,
			prog:        prog,
		}
		go w.run()
	}

	lq := make(LinkQueue, 1, 1024)
	lq[0] = &Link{url: mainUrl}

	sch := &scheduler{
		wc:   wc,
		ec:   ec,
		sc:   sc,
		pc:   pc,
		wg:   &wg,
		lq:   lq,
		seen: make(map[string]struct{}),
		prog: prog,
	}

	go sch.run()

	wg.Wait()
	prog.Send(msgDone{})
}

package main

import (
	"container/heap"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/briandowns/spinner"
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

var recurse = flag.Bool("recurse", false, "recurse into linked pages with same domain")
var dryrun = flag.Bool("dryrun", false, "dry run")
var output = flag.String("output", "", "output directory (has to exist)")

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

func (lq *LinkQueue) Push(x interface{}) {
	n := len(*lq)
	item := x.(*Link)
	item.index = n
	*lq = append(*lq, item)
}

func (lq *LinkQueue) Pop() interface{} {
	old := *lq
	n := len(old)
	item := old[n-1]
	item.index = -1 // for safety
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
}

func (sch *scheduler) run() {
	numInflight := 0

	for {
		select {
		case lk := <-sch.ec:
			if len(lk.errs) < maxTries {
				heap.Push(&sch.lq, lk)
			} else {
				fmt.Printf("failed to process %s with errors %v\n", lk.url.String(), lk.errs[0])
				numInflight--
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
}

func (w *worker) down(lk *Link) error {
	if DEBUG {
		fmt.Printf("down(%s)\n", lk.url.String())
		return nil
	}
	fid := uuid.NewString()
	filename := path.Base(lk.url.Path)
	if filename == "" {
		return fmt.Errorf("Failed to derive file name from %v", lk.url)
	}
	ext := path.Ext(filename)
	filename = fmt.Sprintf("%s-%s%s", strings.TrimSuffix(filename, ext), fid, ext)

	out, err := os.Create(filepath.Join(*output, filename))
	if err != nil {
		return err
	}
	defer out.Close()

	resp, err := http.Get(lk.url.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}
	return nil
}

func (w *worker) extractLink(n *html.Node) (*Link, error) {
	var urlStr string
	isLeaf := false

	if n.DataAtom == atom.A {
		if !*recurse {
			return nil, nil
		}
		urlStr = scrape.Attr(n, "href")
	} else if n.DataAtom == atom.Img {
		urlStr = scrape.Attr(n, "src")
		if urlStr == "" {
			urlStr = scrape.Attr(n, "data-src")
		}
		isLeaf = true
	} else if n.DataAtom == atom.Frame {
		urlStr = scrape.Attr(n, "src")
	} else {
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

	if DEBUG {
		fmt.Printf("extracting link from %s to %s\n", urlStr, u.String())
	}

	return &Link{
		url:    u,
		isLeaf: isLeaf,
	}, nil
}

func (w *worker) scrape(lk *Link) error {
	resp, err := http.Get(lk.url.String())
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

	for _, n := range nodes {
		lk, err := w.extractLink(n)
		if err != nil {
			return err
		}
		if lk != nil {
			w.sc <- lk
		}
	}
	return nil
}

func (w *worker) process(lk *Link) error {
	if *dryrun {
		fmt.Printf("link: %s\n", lk.url.String())
	}
	if lk.isLeaf {
		if *dryrun {
			return nil
		}
		return w.down(lk)
	} else {
		return w.scrape(lk)
	}
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

func main() {
	flag.Parse()

	urlStr := flag.Arg(0)
	if urlStr == "" {
		fmt.Fprintf(os.Stderr, "Please supply a url!\n")
		fmt.Fprintf(os.Stderr, "usage: %s [options] url\n", os.Args[0])
		os.Exit(0)
	}

	mainUrl, err := url.Parse(urlStr)
	if err != nil || !mainUrl.IsAbs() {
		fmt.Fprintf(os.Stderr, "Please supply a valid absolute url!\n")
		fmt.Fprintf(os.Stderr, "usage: %s [options] url\n", os.Args[0])
		os.Exit(0)
	}

	mainBaseUrl := mainUrl
	mainBaseUrl.Path = ""
	mainBaseUrl.RawQuery = ""
	mainBaseUrl.Fragment = ""

	spn := spinner.New(spinner.CharSets[9], 100*time.Millisecond)
	spn.Start()

	var wg sync.WaitGroup

	wc := make(chan *Link)
	ec := make(chan *Link)
	sc := make(chan *Link)
	pc := make(chan *Link)

	wg.Add(numWorkers + 1)

	for i := 0; i < numWorkers; i++ {
		w := &worker{
			wc:          wc,
			ec:          ec,
			sc:          sc,
			pc:          pc,
			wg:          &wg,
			mainUrl:     mainUrl,
			mainBaseUrl: mainBaseUrl,
		}
		go w.run()
	}

	lq := make(LinkQueue, 1, 1024)
	lq[0] = &Link{
		url: mainUrl,
	}

	sch := &scheduler{
		wc:   wc,
		ec:   ec,
		sc:   sc,
		pc:   pc,
		wg:   &wg,
		lq:   lq,
		seen: make(map[string]struct{}),
	}

	go sch.run()

	wg.Wait()
	spn.Stop()
}

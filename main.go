package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fiatjaf/relayer/v2"
	"github.com/nbd-wtf/go-nostr"
	"go.uber.org/atomic"
)

const (
	urlPattern = `https?://[-A-Za-z0-9+&@#\/%?=~_|!:,.;\(\)]+`
)

type Relay struct {
	URL   string
	Write bool
}

var (
	rewriteURLs bool
	urlRe       = regexp.MustCompile(urlPattern)
)

var relays = []Relay{
	{URL: "wss://nostr.compile-error.net/", Write: true},
	{URL: "wss://relay.nostr.band/", Write: true},
	{URL: "wss://nostr-relay.nokotaro.com/", Write: true},
	{URL: "wss://universe.nostrich.land/?lang=ja&lang=en", Write: false},
	{URL: "wss://relay.nostrich.land/", Write: true},
	{URL: "wss://relay-jp.nostr.wirednet.jp/", Write: true},
	{URL: "wss://nostr.wine/", Write: true},
	{URL: "wss://nostr.h3z.jp/", Write: true},
}

type forwarder struct {
	relays []*nostr.Relay
}

func (d *forwarder) Name() string { return "ForwardOnlyRelay" }
func (d *forwarder) Init() error {
	if len(d.relays) > 0 {
		return nil
	}
	d.relays = make([]*nostr.Relay, len(relays))
	go func() {
		for i := range relays {
			if d.relays[i] == nil {
				rr, err := nostr.RelayConnect(context.TODO(), relays[i].URL)
				if err != nil {
					continue
				}
				d.relays[i] = rr
				log.Println("connected", relays[i])
			} else if d.relays[i].ConnectionError != nil {
				log.Println("closed", relays[i])
				d.relays[i] = nil
				continue
			}
		}
		time.Sleep(10 * time.Second)
	}()
	return nil
}
func (f *forwarder) Storage(ctx context.Context) relayer.Storage {
	return f
}

func (d *forwarder) BeforeSave(ctx context.Context, evt *nostr.Event) {
}

func (d *forwarder) AfterSave(ctx context.Context, evt *nostr.Event) {
}

func (d *forwarder) AcceptEvent(ctx context.Context, evt *nostr.Event) bool {
	return true
}

func (d *forwarder) DeleteEvent(ctx context.Context, id string, pubkey string) error {
	log.Println("DeleteEvent", id)

	fmt.Println(id, pubkey)
	return nil
}

func (d *forwarder) SaveEvent(ctx context.Context, event *nostr.Event) error {
	log.Println("SaveEvent", event.Kind)

	var success atomic.Int64
	var wg sync.WaitGroup
	for i, r := range d.relays {
		if r == nil || !relays[i].Write {
			continue
		}
		wg.Add(1)
		r := r
		go func() {
			defer wg.Done()
			status, err := r.Publish(context.Background(), *event)
			if err != nil {
				log.Println("error", r.URL, err)
				return
			}
			if err == nil && status != nostr.PublishStatusFailed {
				success.Add(1)
			}
		}()
	}
	wg.Wait()
	if success.Load() == 0 {
		return errors.New("cannot post")
	}
	return nil
}

func (d *forwarder) QueryEvents(ctx context.Context, filter *nostr.Filter) (chan *nostr.Event, error) {
	log.Println("QueryEvent", filter.Kinds)

	ch := make(chan *nostr.Event, len(d.relays))
	go func() {
		defer close(ch)

		var wg sync.WaitGroup
		var mu sync.Mutex

		m := make(map[string]*nostr.Event)
		for _, r := range d.relays {
			if r == nil {
				continue
			}
			wg.Add(1)
			r := r
			go func() {
				defer wg.Done()
				log.Printf("send query to %v", r.URL)
				evs, err := r.QuerySync(context.Background(), *filter)
				if err != nil {
					log.Println("error", r.URL, err)
					return
				}
				log.Printf("received result from %v", r.URL)
				for _, ev := range evs {
					mu.Lock()
					m[ev.ID] = ev
					mu.Unlock()
				}
			}()
		}
		wg.Wait()

		ids := []string{}
		for k := range m {
			ids = append(ids, k)
		}
		sort.Slice(ids, func(i, j int) bool {
			return m[ids[i]].CreatedAt < m[ids[j]].CreatedAt
		})
		for _, id := range ids {
			if rewriteURLs {
				matches := urlRe.FindAllStringSubmatchIndex(m[id].Content, -1)
				content := m[id].Content
				result := ""
				for _, m := range matches {
					result += content[:m[0]]
					result += "http://0.0.0.0:7447?url=" + url.QueryEscape(content[m[0]:m[1]])
				}
				if len(matches) > 0 {
					result += content[matches[len(matches)-1][1]:]
				}
				m[id].Content = result
			}
			ch <- m[id]
		}
		log.Printf("received %d events", len(ids))
	}()
	return ch, nil
}

func main() {
	flag.BoolVar(&rewriteURLs, "rewrite", false, "rewrite URLs")
	flag.Parse()

	var r forwarder
	server, err := relayer.NewServer(&r)
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}
	server.Router().HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		u := r.URL.Query().Get("url")
		resp, err := http.Get(u)
		if err != nil {
			log.Println(err)
			return
		}
		defer resp.Body.Close()

		if !strings.HasPrefix(resp.Header.Get("content-type"), "image/") {
			http.Redirect(w, r, u, http.StatusFound)
			return
		}

		for k, v := range resp.Header {
			for _, vv := range v {
				w.Header().Add(k, vv)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})
	if err := server.Start("0.0.0.0", 7447); err != nil {
		log.Fatalf("server terminated: %v", err)
	}
}

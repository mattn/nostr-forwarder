package main

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/fiatjaf/relayer/v2"
	"github.com/nbd-wtf/go-nostr"
)

var relays = []string{
	"wss://nostr-relay.nokotaro.com/",
	"wss://nostr.compile-error.net/",
	"wss://nostr.h3z.jp/",
	"wss://nostr.wine/",
	"wss://relay-jp.nostr.wirednet.jp/",
	"wss://relay.nostr.band/",
	"wss://relay.nostrich.land/",
	"wss://universe.nostrich.land/?lang=ja&lang=en",
}

type forwarder struct {
	relays []*nostr.Relay
	wg     sync.WaitGroup
}

func (d *forwarder) Name() string { return "ForwardOnlyRelay" }
func (d *forwarder) Init() error {
	d.relays = make([]*nostr.Relay, len(relays))
	go func() {
		for i := range relays {
			if d.relays[i] == nil {
				rr, err := nostr.RelayConnect(context.TODO(), relays[i])
				if err != nil {
					continue
				}
				d.relays[i] = rr
			} else if d.relays[i].ConnectionError != nil {
				d.relays[i] = nil
				continue
			}
		}
		time.Sleep(time.Second)
	}()
	return nil
}
func (f *forwarder) Storage(ctx context.Context) relayer.Storage {
	return f
}
func (d *forwarder) BeforeSave(ctx context.Context, evt *nostr.Event)       {}
func (d *forwarder) AfterSave(ctx context.Context, evt *nostr.Event)        {}
func (d *forwarder) AcceptEvent(ctx context.Context, evt *nostr.Event) bool { return true }

func (d *forwarder) DeleteEvent(ctx context.Context, id string, pubkey string) error { return nil }
func (d *forwarder) SaveEvent(ctx context.Context, event *nostr.Event) error         { return nil }
func (d *forwarder) QueryEvents(ctx context.Context, filter *nostr.Filter) (chan *nostr.Event, error) {
	m := make(map[string]*nostr.Event)
	for _, r := range d.relays {
		if r == nil {
			continue
		}
		evs, err := r.QuerySync(context.TODO(), *filter)
		if err != nil {
			continue
		}
		for _, ev := range evs {
			m[ev.ID] = ev
		}
	}
	ids := []string{}
	for k := range m {
		ids = append(ids, k)
	}
	sort.Slice(ids, func(i, j int) bool {
		return m[ids[i]].CreatedAt < m[ids[j]].CreatedAt
	})
	ch := make(chan *nostr.Event, len(d.relays))
	go func() {
		defer close(ch)
		for _, id := range ids {
			ch <- m[id]
		}
	}()
	return ch, nil
}

func main() {
	var r forwarder
	server, err := relayer.NewServer(&r)
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}
	if err := server.Start("0.0.0.0", 7447); err != nil {
		log.Fatalf("server terminated: %v", err)
	}
}

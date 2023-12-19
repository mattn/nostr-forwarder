package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"sync"
	"sync/atomic"

	"github.com/fiatjaf/eventstore"
	"github.com/fiatjaf/relayer/v2"
	"github.com/nbd-wtf/go-nostr"
)

const (
	urlPattern = `https?://[-A-Za-z0-9+&@#\/%?=~_|!:,.;\(\)]+`
)

type forwarder struct {
	ctx    context.Context
	relays []string
	pool   *nostr.SimplePool
}

var (
	_ relayer.Relay = (*forwarder)(nil)
)

func (d *forwarder) Name() string {
	return "ForwardOnlyRelay"
}

func (d *forwarder) Init() error {
	d.ctx = context.Background()
	d.pool = nostr.NewSimplePool(d.ctx)
	for _, v := range d.relays {
		_, err := d.pool.EnsureRelay(v)
		if err != nil {
			log.Println(err)
		}
	}
	return nil
}

func (f *forwarder) Storage(ctx context.Context) eventstore.Store {
	return f
}

func (f *forwarder) Close() {
}

func (d *forwarder) DeleteEvent(ctx context.Context, ev *nostr.Event) error {
	return nil
}

func (d *forwarder) AcceptEvent(ctx context.Context, evt *nostr.Event) bool {
	return true
}

func (d *forwarder) SaveEvent(ctx context.Context, event *nostr.Event) error {
	var success atomic.Int64
	var wg sync.WaitGroup
	d.pool.Relays.Range(func(k string, v *nostr.Relay) bool {
		wg.Add(1)
		go func(v *nostr.Relay) {
			defer wg.Done()
			err := v.Publish(context.Background(), *event)
			if err != nil {
				log.Println("error", v.URL, err)
				return
			}
			success.Add(1)
		}(v)
		return true
	})

	wg.Wait()
	if success.Load() == 0 {
		return errors.New("cannot post")
	}
	return nil
}

func (d *forwarder) QueryEvents(ctx context.Context, filter nostr.Filter) (chan *nostr.Event, error) {
	log.Println("QueryEvent", filter.Kinds)

	ch := make(chan *nostr.Event)
	sub := d.pool.SubMany(context.Background(), d.relays, nostr.Filters{filter})
	go func() {
		defer close(ch)
		for {
			v, ok := <-sub
			if !ok {
				break
			}
			ch <- v.Event
		}
	}()
	return ch, nil
}

func main() {
	flag.Parse()

	r := forwarder{
		relays: []string{
			"wss://nostr.compile-error.net/",
			"wss://relay.nostr.band/",
			"wss://nostr-relay.nokotaro.com/",
			"wss://relay-jp.nostr.wirednet.jp/",
			"wss://nostr.wine/",
			"wss://yabu.me/",
		},
	}
	server, err := relayer.NewServer(&r)
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}
	if err := server.Start("0.0.0.0", 7447); err != nil {
		log.Fatalf("server terminated: %v", err)
	}
}

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

type forwarder struct {
	relays []string
	pool   *nostr.SimplePool
}

type storage struct {
	f *forwarder
}

var (
	_ relayer.Relay = (*forwarder)(nil)
)

func (f *forwarder) Name() string {
	return "ForwardOnlyRelay"
}

func (f *forwarder) Init() error {
	f.pool = nostr.NewSimplePool(context.Background())
	for _, v := range f.relays {
		go func(v string) {
			log.Println(v, "connecting")
			_, err := f.pool.EnsureRelay(v)
			if err != nil {
				log.Println(v, err)
			} else {
				log.Println(v, "connected")
			}
		}(v)
	}
	return nil
}

func (s *storage) Init() error {
	return nil
}

func (f *forwarder) Storage(ctx context.Context) eventstore.Store {
	return &storage{
		f: f,
	}
}

func (f *storage) Close() {
}

func (s *forwarder) AcceptEvent(ctx context.Context, ev *nostr.Event) bool {
	return true
}

func (s *storage) DeleteEvent(ctx context.Context, ev *nostr.Event) error {
	return nil
}

func (s *storage) SaveEvent(ctx context.Context, evt *nostr.Event) error {
	var success atomic.Int64
	var wg sync.WaitGroup
	s.f.pool.Relays.Range(func(k string, v *nostr.Relay) bool {
		wg.Add(1)
		go func(v *nostr.Relay) {
			defer wg.Done()
			err := v.Publish(context.Background(), *evt)
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

func (s *storage) QueryEvents(ctx context.Context, filter nostr.Filter) (chan *nostr.Event, error) {
	log.Println("QueryEvent", filter.Kinds)

	ch := make(chan *nostr.Event, 5)
	sub := s.f.pool.SubMany(context.Background(), s.f.relays, nostr.Filters{filter})
	go func() {
		defer close(ch)
		for {
			v, ok := <-sub
			if !ok {
				break
			}
			log.Println(v.Event)
			ch <- v.Event
		}
	}()
	return ch, nil
}

type arrayFlags []string

func (i *arrayFlags) String() string {
	return "relays"
}

func (i *arrayFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}

func main() {
	var relays arrayFlags = []string{
		"wss://nostr.compile-error.net/",
		"wss://relay.nostr.band/",
		"wss://nostr-relay.nokotaro.com/",
		"wss://relay-jp.nostr.wirednet.jp/",
		"wss://nostr.wine/",
		"wss://yabu.me/",
	}
	flag.Var(&relays, "relay", "multiple relays can be specified")
	flag.Parse()

	r := forwarder{}
	server, err := relayer.NewServer(&r)
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}
	if err := server.Start("0.0.0.0", 7447); err != nil {
		log.Fatalf("server terminated: %v", err)
	}
}

package traffic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/vishvananda/netlink"

	terr "tcsss/internal/errors"
)

// Watch listens to netlink events and reapplies traffic shaping when needed.
func (s *Shaper) Watch(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			if s.logger != nil {
				s.logger.Error("watcher panic recovered",
					slog.Any("panic", r),
					slog.String("stack", string(stack)))
			}
			if err == nil {
				err = fmt.Errorf("watcher panic: %v", r)
			}
		}
	}()

	subs, err := s.setupNetlinkSubscriptions()
	if err != nil {
		return err
	}
	defer subs.Close()

	return s.watchLoop(ctx, subs)
}

type netlinkSubscriptions struct {
	links     chan netlink.LinkUpdate
	addrs     chan netlink.AddrUpdate
	linkDone  chan struct{}
	addrDone  chan struct{}
	closeOnce sync.Once
}

func (s *netlinkSubscriptions) Close() {
	s.closeOnce.Do(func() {
		close(s.linkDone)
		close(s.addrDone)
	})
}

func (s *Shaper) setupNetlinkSubscriptions() (*netlinkSubscriptions, error) {
	subs := &netlinkSubscriptions{
		links:    make(chan netlink.LinkUpdate, 32),
		addrs:    make(chan netlink.AddrUpdate, 32),
		linkDone: make(chan struct{}),
		addrDone: make(chan struct{}),
	}

	if err := s.netlink.LinkSubscribeWithOptions(subs.links, subs.linkDone, netlink.LinkSubscribeOptions{ListExisting: false}); err != nil {
		subs.Close()
		return nil, terr.New(
			terr.CategoryCritical,
			fmt.Errorf("subscribe link: %w", err),
			terr.ErrorContext{Operation: "netlink_link_subscribe"},
		)
	}
	if err := s.netlink.AddrSubscribeWithOptions(subs.addrs, subs.addrDone, netlink.AddrSubscribeOptions{ListExisting: false}); err != nil {
		subs.Close()
		return nil, terr.New(
			terr.CategoryCritical,
			fmt.Errorf("subscribe addr: %w", err),
			terr.ErrorContext{Operation: "netlink_addr_subscribe"},
		)
	}

	return subs, nil
}

func (s *Shaper) watchLoop(ctx context.Context, subs *netlinkSubscriptions) error {
	applyTicker := time.NewTicker(s.reapplyInterval)
	cleanupTicker := time.NewTicker(s.cleanupInterval)
	defer applyTicker.Stop()
	defer cleanupTicker.Stop()

	pending := newPendingChanges(s.netlink)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case update, ok := <-subs.links:
			if !ok {
				return errors.New("link subscription closed")
			}
			pending.AddLink(update)
		case update, ok := <-subs.addrs:
			if !ok {
				return errors.New("addr subscription closed")
			}
			pending.AddAddr(update)
		case <-applyTicker.C:
			if err := s.applyPending(ctx, pending); err != nil && !errors.Is(err, context.Canceled) {
				s.handleCategorizedError("reapply failed", "", err, terr.CategoryRecoverable)
			}
		case <-cleanupTicker.C:
			if err := s.cleanupStaleSignatures(); err != nil {
				s.handleCategorizedError("cleanup stale signatures failed", "", err, terr.CategoryRecoverable)
			}
		}
	}
}

type pendingChanges struct {
	mu      sync.Mutex
	all     bool
	names   map[string]struct{}
	netlink NetlinkClient
}

func newPendingChanges(netlinkClient NetlinkClient) *pendingChanges {
	return &pendingChanges{
		names:   map[string]struct{}{},
		netlink: netlinkClient,
	}
}

func (p *pendingChanges) AddLink(update netlink.LinkUpdate) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.all {
		return
	}
	if attrs := update.Attrs(); attrs != nil && attrs.Name != "" {
		p.addNameLocked(attrs.Name)
		return
	}
	if link := update.Link; link != nil {
		if linkAttrs := link.Attrs(); linkAttrs != nil && linkAttrs.Name != "" {
			p.addNameLocked(linkAttrs.Name)
			return
		}
	}
	p.markAllLocked()
}

func (p *pendingChanges) AddAddr(update netlink.AddrUpdate) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.all {
		return
	}
	if p.netlink != nil {
		if attrs, err := safeGetLinkAttrs(p.netlink, update.LinkIndex); err == nil && attrs.Name != "" {
			p.addNameLocked(attrs.Name)
			return
		}
	} else {
		if attrs, err := safeGetLinkAttrs(defaultNetlinkClient{}, update.LinkIndex); err == nil && attrs.Name != "" {
			p.addNameLocked(attrs.Name)
			return
		}
	}
	p.markAllLocked()
}

func (p *pendingChanges) addNameLocked(name string) {
	if p.names == nil {
		p.names = map[string]struct{}{}
	}
	p.names[name] = struct{}{}
}

func (p *pendingChanges) markAllLocked() {
	p.all = true
	p.names = map[string]struct{}{}
}

func (p *pendingChanges) snapshot() (bool, map[string]struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.all {
		return true, nil
	}
	if len(p.names) == 0 {
		return false, nil
	}
	copyNames := make(map[string]struct{}, len(p.names))
	for name := range p.names {
		copyNames[name] = struct{}{}
	}
	return false, copyNames
}

func (p *pendingChanges) clear() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.all = false
	p.names = map[string]struct{}{}
}

func (s *Shaper) applyPending(ctx context.Context, pending *pendingChanges) error {
	applyAll, names := pending.snapshot()
	if !applyAll && len(names) == 0 {
		return nil
	}
	pending.clear()

	ctxApply, cancel := context.WithTimeout(ctx, s.applyTimeout)
	defer cancel()

	if applyAll {
		return s.applyInterfaces(ctxApply, nil)
	}
	return s.applyInterfaces(ctxApply, names)
}

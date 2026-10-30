package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// 2026-10-30 audit B4 (members-only gate on a cache hit) and B10 (an
// unreachable Redis must cost the read a short bound, then Postgres).

// respServer is the smallest Redis the cache needs: GET / SET / DEL over
// RESP2, every other command (HELLO, CLIENT SETINFO) refused or OK'd the way
// a real server would. Nothing here is a database.
type respServer struct {
	ln   net.Listener
	mu   sync.Mutex
	data map[string]string
}

func newRESPServer(t *testing.T) *respServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &respServer{ln: ln, data: map[string]string{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *respServer) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	for {
		args, err := readRESPArray(r)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		var reply string
		s.mu.Lock()
		switch strings.ToUpper(args[0]) {
		case "GET":
			if v, ok := s.data[args[1]]; ok {
				reply = fmt.Sprintf("$%d\r\n%s\r\n", len(v), v)
			} else {
				reply = "$-1\r\n"
			}
		case "SET":
			s.data[args[1]] = args[2]
			reply = "+OK\r\n"
		case "DEL":
			delete(s.data, args[1])
			reply = ":1\r\n"
		case "PING":
			reply = "+PONG\r\n"
		case "CLIENT":
			reply = "+OK\r\n"
		default:
			reply = "-ERR unknown command\r\n"
		}
		s.mu.Unlock()
		if _, err := io.WriteString(c, reply); err != nil {
			return
		}
	}
}

func readRESPArray(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("not an array: %q", line)
	}
	n, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		hdr, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(strings.TrimSpace(hdr[1:]))
		if err != nil {
			return nil, err
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		out = append(out, string(buf[:size]))
	}
	return out, nil
}

// A body cached before its author made it members-only comes back gated:
// the cache hit re-reads tier_required_id from Postgres every time.
func TestCachedPostBody_RevalidatesTheMembersOnlyGateOnEveryHit(t *testing.T) {
	srv := newRESPServer(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.ln.Addr().String()})
	t.Cleanup(func() { _ = rdb.Close() })

	id, tier := uuid.New(), uuid.New()
	cached := postgres.Post{ID: id, AuthorID: uuid.New(), Visibility: "public", ReviewStatus: "approved", Text: "members only now"}
	raw, _ := json.Marshal(cached)
	srv.data[postCacheKey(id)] = string(raw)

	store := newFakeListReads()
	store.access[id] = &postgres.PostAccessState{ReviewStatus: "approved", Visibility: "public", TierRequiredID: &tier}
	s := &Service{rdb: rdb, listReadSeam: store}

	got, err := s.getCachedPostBody(context.Background(), id)
	if err != nil || got == nil {
		t.Fatalf("cache hit: %v %v", got, err)
	}
	if store.getPosts != 0 {
		t.Fatal("precondition: this read must be a cache hit")
	}
	if got.TierRequiredID == nil || *got.TierRequiredID != tier {
		t.Fatalf("a members-only post was served ungated from the cache: tier=%v", got.TierRequiredID)
	}

	// The gate cleared since: the hit reads it as open again.
	store.access[id] = &postgres.PostAccessState{ReviewStatus: "approved", Visibility: "public"}
	gated := cached
	gated.TierRequiredID = &tier
	raw, _ = json.Marshal(gated)
	srv.mu.Lock()
	srv.data[postCacheKey(id)] = string(raw)
	srv.mu.Unlock()
	got, err = s.getCachedPostBody(context.Background(), id)
	if err != nil || got == nil || got.TierRequiredID != nil {
		t.Fatalf("a cleared gate stayed on the cached body: %+v %v", got, err)
	}
}

// A Redis that accepts connections and never answers (dropped packets) costs
// the read the cache's short bound, then Postgres answers — not the client's
// dial / read timeouts times its retries.
func TestCachedPostBody_UnreachableRedisFallsBackFast(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c) // never read, never answered
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})
	rdb := redis.NewClient(&redis.Options{Addr: ln.Addr().String()})
	t.Cleanup(func() { _ = rdb.Close() })

	id := uuid.New()
	store := newFakeListReads()
	store.posts[id] = &postgres.Post{ID: id, AuthorID: uuid.New(), Visibility: "public", ReviewStatus: "approved"}
	s := &Service{rdb: rdb, listReadSeam: store}

	start := time.Now()
	got, err := s.getCachedPostBody(context.Background(), id)
	elapsed := time.Since(start)
	if err != nil || got == nil || got.ID != id {
		t.Fatalf("the read did not fall back to Postgres: %v %v", got, err)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("an unreachable Redis held the read for %v; the cache must give up within its short bound", elapsed)
	}
}

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/nkanaev/yarr/src/storage"
	"github.com/nkanaev/yarr/src/storage/model"
)

func TestStatic(t *testing.T) {
	handler := NewServer("127.0.0.1:8000").Handler()
	url := "/static/bundle.js"

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", url, nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Result().StatusCode != 200 {
		t.FailNow()
	}
}

func TestStaticWithBase(t *testing.T) {
	server := NewServer("127.0.0.1:8000")
	server.BasePath = "/sub"

	handler := server.Handler()
	url := "/sub/static/bundle.js"

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", url, nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Result().StatusCode != 200 {
		t.FailNow()
	}
}

func TestStaticBanTemplates(t *testing.T) {
	handler := NewServer("127.0.0.1:8000").Handler()
	url := "/static/login.html"

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", url, nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Result().StatusCode != 404 {
		t.FailNow()
	}
}

func TestIndexGzipped(t *testing.T) {
	log.SetOutput(io.Discard)
	db, _ := storage.New(":memory:")
	log.SetOutput(os.Stderr)
	server := NewServer("127.0.0.1:8000")
	server.Storage = NewLocalStorage(db)
	handler := server.Handler()
	url := "/"

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", url, nil)
	request.Header.Set("accept-encoding", "gzip")
	handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	if response.StatusCode != 200 {
		t.FailNow()
	}
	if response.Header.Get("content-encoding") != "gzip" {
		t.Errorf("invalid content-encoding header: %#v", response.Header.Get("content-encoding"))
	}
	if response.Header.Get("content-type") != "text/html" {
		t.Errorf("invalid content-type header: %#v", response.Header.Get("content-type"))
	}
}

func TestExtractReadableContent(t *testing.T) {
	t.Run("returns sanitized content for a real article", func(t *testing.T) {
		body := `<html><body><article><p>` + strings.Repeat("meaningful text ", 20) + `</p></article></body></html>`
		content, ok := extractReadableContent("http://example.com", body)
		if !ok {
			t.Fatal("expected extraction to succeed")
		}
		if !strings.Contains(content, "meaningful text") {
			t.Fatalf("expected content to contain article text, got %q", content)
		}
	})

	t.Run("fails when the page has no extractable text", func(t *testing.T) {
		body := `<html><body><script>doStuff()</script></body></html>`
		content, ok := extractReadableContent("http://example.com", body)
		if ok {
			t.Fatalf("expected extraction to fail, got content %q", content)
		}
		if content != "" {
			t.Fatalf("expected empty content on failure, got %q", content)
		}
	})
}

func TestFeedUpdateReadability(t *testing.T) {
	log.SetOutput(io.Discard)
	defer log.SetOutput(os.Stderr)

	db, err := storage.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer("127.0.0.1:8000")
	server.Storage = NewLocalStorage(db)
	handler := server.Handler()

	feed := db.CreateFeed(model.CreateFeedParams{Title: "feed", FeedLink: "http://example.com/feed.xml"})
	if feed.Readability {
		t.Fatal("expected readability to default to false")
	}

	body := `{"readability":true}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("PUT", fmt.Sprintf("/api/feeds/%d", feed.Id), strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)

	if recorder.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Result().StatusCode)
	}
	if updated := db.GetFeed(feed.Id); !updated.Readability {
		t.Fatal("expected readability to be enabled")
	}
}

func TestFeedCreateWithTitleOverride(t *testing.T) {
	log.SetOutput(io.Discard)
	defer log.SetOutput(os.Stderr)

	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(`<?xml version="1.0"?>
			<rss version="2.0">
				<channel>
					<title>RSS Title</title>
					<link>http://example.com</link>
					<item>
						<title>Item 1</title>
						<link>http://example.com/1</link>
					</item>
				</channel>
			</rss>
		`))
	}))
	defer feedSrv.Close()

	db, err := storage.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer("127.0.0.1:8000")
	server.Storage = NewLocalStorage(db)
	handler := server.Handler()

	t.Run("override title", func(t *testing.T) {
		body := fmt.Sprintf(`{"url":%q,"title_override":"Override Title"}`, feedSrv.URL)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/api/feeds", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)

		if recorder.Result().StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", recorder.Result().StatusCode)
		}

		var resp map[string]any
		json.NewDecoder(recorder.Result().Body).Decode(&resp)
		if resp["status"] != "success" {
			t.Fatalf("expected success, got %v", resp["status"])
		}
		feed := resp["feed"].(map[string]any)
		if feed["title"] != "Override Title" {
			t.Fatalf("expected 'Override Title', got %v", feed["title"])
		}
	})

	t.Run("no override uses rss title", func(t *testing.T) {
		body := fmt.Sprintf(`{"url":%q}`, feedSrv.URL)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/api/feeds", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)

		if recorder.Result().StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", recorder.Result().StatusCode)
		}

		var resp map[string]any
		json.NewDecoder(recorder.Result().Body).Decode(&resp)
		if resp["status"] != "success" {
			t.Fatalf("expected success, got %v", resp["status"])
		}
		feed := resp["feed"].(map[string]any)
		if feed["title"] != "RSS Title" {
			t.Fatalf("expected 'RSS Title', got %v", feed["title"])
		}
	})
}

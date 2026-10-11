package textfetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStripHTMLRemovesTagsScriptsAndStyles(t *testing.T) {
	html := `<html><head><style>.a{color:red}</style></head>
<body>
<script>console.log("hi")</script>
<h1>Senior Backend Engineer</h1>
<p>We are <strong>hiring</strong> a backend engineer.</p>
</body></html>`
	got := StripHTML(html)
	if strings.Contains(got, "console.log") {
		t.Errorf("StripHTML left script content in: %q", got)
	}
	if strings.Contains(got, "color:red") {
		t.Errorf("StripHTML left style content in: %q", got)
	}
	want := "Senior Backend Engineer We are hiring a backend engineer."
	if got != want {
		t.Errorf("StripHTML() = %q, want %q", got, want)
	}
}

func TestFetchReturnsPlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("request had no User-Agent header")
		}
		w.Write([]byte("<p>Hello job seeker</p>"))
	}))
	defer srv.Close()

	text, err := Fetch(context.Background(), nil, srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if text != "Hello job seeker" {
		t.Errorf("Fetch() = %q, want %q", text, "Hello job seeker")
	}
}

func TestFetchReturnsHTTPStatusErrorOn403And429(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		_, err := Fetch(context.Background(), nil, srv.URL)
		srv.Close()
		if err == nil {
			t.Fatalf("status %d: want error, got nil", code)
		}
		statusErr, ok := err.(*HTTPStatusError)
		if !ok {
			t.Fatalf("status %d: want *HTTPStatusError, got %T: %v", code, err, err)
		}
		if statusErr.StatusCode != code {
			t.Errorf("StatusCode = %d, want %d", statusErr.StatusCode, code)
		}
	}
}

func TestFetchCapsLength(t *testing.T) {
	long := strings.Repeat("a", MaxChars+5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(long))
	}))
	defer srv.Close()

	text, err := Fetch(context.Background(), nil, srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(text) != MaxChars {
		t.Errorf("len(text) = %d, want %d", len(text), MaxChars)
	}
}

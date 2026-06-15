package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/zkrebbekx/filtrx"
)

// Server wires HTTP routes to the Store.
type Server struct{ store *Store }

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	// Reads
	mux.HandleFunc("GET /articles", s.listArticles)          // ?status=&title_like=&views_gte=&sort=&first=&total=
	mux.HandleFunc("GET /articles/feed", s.articleFeed)      // ?size=&after=  (keyset / Relay)
	mux.HandleFunc("GET /articles/search", s.searchArticles) // ?q=
	mux.HandleFunc("GET /articles/by-tag", s.articlesByTag)  // ?tag=go&tag=performance
	mux.HandleFunc("GET /articles/by-author", s.byAuthor)    // ?name=
	mux.HandleFunc("GET /authors/published", s.publishedAuthors)
	mux.HandleFunc("GET /stats/articles-per-author", s.articleStats) // ?min=
	// Writes (CRUD)
	mux.HandleFunc("POST /articles", s.createArticle)
	mux.HandleFunc("PATCH /articles/{id}", s.updateArticle)
	mux.HandleFunc("DELETE /articles/{id}", s.deleteArticle) // ?purge=true for a hard delete
	return mux
}

func (s *Server) listArticles(w http.ResponseWriter, r *http.Request) {
	items, info, err := s.store.ListArticles(r.Context(), r.URL.Query())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"articles": items, "pageInfo": info})
}

func (s *Server) articleFeed(w http.ResponseWriter, r *http.Request) {
	conn, err := s.store.ArticleFeed(r.Context(), r.URL.Query())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, conn)
}

func (s *Server) searchArticles(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.SearchArticles(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"articles": items})
}

func (s *Server) articlesByTag(w http.ResponseWriter, r *http.Request) {
	tags := r.URL.Query()["tag"]
	if len(tags) == 1 {
		tags = strings.Split(tags[0], ",")
	}
	items, err := s.store.ArticlesByTag(r.Context(), tags)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"articles": items})
}

func (s *Server) byAuthor(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ArticlesByAuthorName(r.Context(), r.URL.Query().Get("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"articles": items})
}

func (s *Server) publishedAuthors(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.PublishedAuthors(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authors": items})
}

func (s *Server) articleStats(w http.ResponseWriter, r *http.Request) {
	min := 1
	if v := r.URL.Query().Get("min"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			min = n
		}
	}
	items, err := s.store.ArticleCountsByAuthor(r.Context(), min)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"counts": items})
}

func (s *Server) createArticle(w http.ResponseWriter, r *http.Request) {
	var a Article
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if a.Status == "" {
		a.Status = "draft"
	}
	if err := s.store.CreateArticle(r.Context(), &a); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) updateArticle(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var set map[string]any
	if err := json.NewDecoder(r.Body).Decode(&set); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	n, err := s.store.UpdateArticle(r.Context(), id, set)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": n})
}

func (s *Server) deleteArticle(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var n int64
	if r.URL.Query().Get("purge") == "true" {
		n, err = s.store.PurgeArticle(r.Context(), id)
	} else {
		n, err = s.store.SoftDeleteArticle(r.Context(), id)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr maps a filtrx compile error (bad request input) to 400 and anything
// else to 500.
func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, filtrx.ErrCompile) {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

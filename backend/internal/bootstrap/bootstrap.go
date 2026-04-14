package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/EHLO1/project-dysfunctional/backend/internal/config"
)

type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

var usersDB = []User{
	{ID: 1, Username: "gopher_fan", Email: "gopher@example.com"},
	{ID: 2, Username: "svelte_wizard", Email: "wizard@example.com"},
}

func Bootstrap(ctx context.Context) error {
	cfg := config.Load()

	slog.InfoContext(ctx, "Callout is starting")

	server := &http.Server{
		Addr:    cfg.ListenAddr(),
		Handler: router(),
	}

	appCtx, cancelApp := context.WithCancel(ctx)
	defer cancelApp()

	err := startServer(appCtx, server)
	if err != nil {
		return fmt.Errorf("failed to run server: %w", err)
	}

	return nil
}

func startServer(appCtx context.Context, server *http.Server) error {
	go func() {
		var err error

		err = server.ListenAndServe()

		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.ErrorContext(appCtx, "Failed to start server", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		slog.InfoContext(appCtx, "Received shutdown signal")
	case <-appCtx.Done():
		slog.InfoContext(appCtx, "Context canceled")
	}

	// Use background context for shutdown as appCtx is already canceled
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second) //nolint:contextcheck
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil { //nolint:contextcheck
		slog.ErrorContext(shutdownCtx, "Server forced to shutdown", "error", err) //nolint:contextcheck
		return err
	}

	slog.InfoContext(shutdownCtx, "Server stopped gracefully") //nolint:contextcheck

	return nil
}

func router() http.Handler {
	// Initialize the Chi router
	r := chi.NewRouter()

	// Middleware from Chi
	// These add unique log lines for visitors in the order they are listed
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello, world!"))
	})

	r.Route("/api/users", func(r chi.Router) {
		r.Get("/", listUsersHandler)
		r.Get("/{userID}", getUserHandler)
		r.Post("/", createUserHandler)
	})

	return r
}

func listUsersHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(usersDB); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func getUserHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	idParam := chi.URLParam(r, "userID")

	userID, err := strconv.Atoi(idParam)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	for _, user := range usersDB {
		if user.ID == userID {
			json.NewEncoder(w).Encode(user)
			return
		}
	}

	http.Error(w, "User not found", http.StatusNotFound)
}

func createUserHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var newUser User

	if err := json.NewDecoder(r.Body).Decode(&newUser); err != nil {
		http.Error(w, "Invalid JSON Payload", http.StatusBadRequest)
		return
	}

	newUser.ID = len(usersDB) + 1
	usersDB = append(usersDB, newUser)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(newUser)
}

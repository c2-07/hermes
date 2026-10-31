package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "modernc.org/sqlite"
)

type Submission struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

func getConnection() *sql.DB {
	db, err := sql.Open(
		"sqlite",
		"file:database.db?_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)",
	)
	if err != nil {
		log.Fatal(err)
	}

	_, err = db.Exec(`PRAGMA journal_mode=WAL`)
	if err != nil {
		log.Fatal(err)
	}

	db.SetMaxOpenConns(5)

	if err := db.Ping(); err != nil {
		log.Fatal(err)
	}

	return db
}

func createTable(db *sql.DB) {
	stmt := `
		CREATE TABLE IF NOT EXISTS submissions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			message TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`

	if _, err := db.Exec(stmt); err != nil {
		log.Fatal(err)
	}
}

func submit(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var data Submission

		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if data.Name == "" || data.Message == "" {
			http.Error(w, "Missing fields", http.StatusBadRequest)
			return
		}

		result, err := db.ExecContext(
			r.Context(),
			`INSERT INTO submissions (name, message) VALUES (?, ?)`,
			data.Name,
			data.Message,
		)

		if err != nil {
			log.Printf("INSERT ERROR: %v", err)
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		id, err := result.LastInsertId()
		if err != nil {
			http.Error(w, "Failed to retrieve ID", http.StatusInternalServerError)
			return
		}

		data.ID = id
		respondJSON(w, http.StatusCreated, data)
	}
}

func getSubmissions(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.QueryContext(
			r.Context(),
			`SELECT id, name, message FROM submissions ORDER BY id DESC`,
		)

		if err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		submissions := []Submission{}

		for rows.Next() {
			var s Submission

			if err := rows.Scan(&s.ID, &s.Name, &s.Message); err != nil {
				http.Error(w, "Failed to read submissions", http.StatusInternalServerError)
				return
			}

			submissions = append(submissions, s)
		}

		if err := rows.Err(); err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		respondJSON(w, http.StatusOK, submissions)
	}
}

func getSubmission(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}

		var s Submission

		err = db.QueryRowContext(
			r.Context(),
			`SELECT id, name, message FROM submissions WHERE id = ?`,
			id,
		).Scan(&s.ID, &s.Name, &s.Message)

		if err == sql.ErrNoRows {
			http.Error(w, "Submission not found", http.StatusNotFound)
			return
		}

		if err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		respondJSON(w, http.StatusOK, s)
	}
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func main() {
	db := getConnection()
	defer db.Close()

	createTable(db)

	r := chi.NewRouter()
	r.Use(middleware.Logger)

	r.Post("/submit", submit(db))
	r.Get("/submissions", getSubmissions(db))
	r.Get("/submissions/{id}", getSubmission(db))

	log.Println("Server running on :3000")
	log.Fatal(http.ListenAndServe(":3000", r))
}

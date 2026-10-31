package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"
)

type Submission struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

func getConnection() *sql.DB {
	databasePath := os.Getenv("HERMES_DB")
	if databasePath == "" {
		databasePath = "database.db"
	}
	synchronous := os.Getenv("HERMES_SQLITE_SYNCHRONOUS")
	if synchronous == "" {
		synchronous = "NORMAL"
	}
	journalMode := os.Getenv("HERMES_SQLITE_JOURNAL_MODE")
	if journalMode == "" {
		journalMode = "WAL"
	}
	lockingMode := os.Getenv("HERMES_SQLITE_LOCKING_MODE")
	if lockingMode == "" {
		lockingMode = "EXCLUSIVE"
	}
	tempStore := os.Getenv("HERMES_SQLITE_TEMP_STORE")
	if tempStore == "" {
		tempStore = "MEMORY"
	}
	cacheSize := os.Getenv("HERMES_SQLITE_CACHE_SIZE")
	if cacheSize == "" {
		cacheSize = "-65536"
	}
	mmapSize := os.Getenv("HERMES_SQLITE_MMAP_SIZE")
	if mmapSize == "" {
		mmapSize = "268435456"
	}

	db, err := sql.Open(
		"sqlite3",
		"file:"+databasePath+"?_journal_mode="+journalMode+"&_synchronous="+synchronous+"&_locking_mode="+lockingMode+"&_busy_timeout=5000&_temp_store="+tempStore+"&_cache_size="+cacheSize+"&_mmap_size="+mmapSize,
	)
	if err != nil {
		log.Fatal(err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

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

func submit(insert *sql.Stmt) http.HandlerFunc {
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

		result, err := insert.ExecContext(
			r.Context(),
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

func submitBatch(db *sql.DB, insert *sql.Stmt) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var data []Submission
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		if len(data) == 0 {
			http.Error(w, "At least one submission is required", http.StatusBadRequest)
			return
		}
		for _, submission := range data {
			if submission.Name == "" || submission.Message == "" {
				http.Error(w, "Missing fields", http.StatusBadRequest)
				return
			}
		}

		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		batchInsert := tx.Stmt(insert)
		for index := range data {
			result, err := batchInsert.ExecContext(
				r.Context(),
				data[index].Name,
				data[index].Message,
			)
			if err != nil {
				tx.Rollback()
				http.Error(w, "Database error", http.StatusInternalServerError)
				return
			}

			data[index].ID, err = result.LastInsertId()
			if err != nil {
				tx.Rollback()
				http.Error(w, "Failed to retrieve ID", http.StatusInternalServerError)
				return
			}
		}

		if err := tx.Commit(); err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		if r.URL.Query().Get("response") == "none" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

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

	insert, err := db.Prepare(`INSERT INTO submissions (name, message) VALUES (?, ?)`)
	if err != nil {
		log.Fatal(err)
	}
	defer insert.Close()

	r := chi.NewRouter()

	r.Post("/submit", submit(insert))
	r.Post("/submit/batch", submitBatch(db, insert))
	r.Get("/submissions", getSubmissions(db))
	r.Get("/submissions/{id}", getSubmission(db))

	address := os.Getenv("HERMES_ADDR")
	if address == "" {
		address = ":3000"
	}

	log.Printf("Server running on %s", address)
	log.Fatal(http.ListenAndServe(address, r))
}

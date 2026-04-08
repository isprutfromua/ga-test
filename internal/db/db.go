package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/isprutfromua/ga-test/internal/config"
)

func Open(cfg config.DatabaseConfig) (*sql.DB, error) {
	db, err := sql.Open("pgx", cfg.URL)
	if err != nil { return nil, err }
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := retry(10, 2*time.Second, func() error { return db.Ping() }); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func Migrate(db *sql.DB, migrationsPath string) error {
	entries, err := os.ReadDir(migrationsPath)
	if err != nil { return err }
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".up.sql") { continue }
		files = append(files, filepath.Join(migrationsPath, name))
	}
	sort.Strings(files)
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil { return err }
		if err := execSQL(db, string(content)); err != nil { return fmt.Errorf("applying %s: %w", filepath.Base(path), err) }
	}
	return nil
}

func retry(attempts int, delay time.Duration, fn func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		err = fn()
		if err == nil { return nil }
		time.Sleep(delay)
	}
	return err
}

func execSQL(db *sql.DB, sqlText string) error {
	parts := strings.Split(sqlText, ";")
	for _, part := range parts {
		stmt := strings.TrimSpace(part)
		if stmt == "" || strings.HasPrefix(stmt, "--") { continue }
		if _, err := db.Exec(stmt); err != nil { return err }
	}
	return nil
}

var ErrNoChange = errors.New("no change")

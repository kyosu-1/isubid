package main

import (
	"fmt"
	"os"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func dbDSN(multiStatements bool) string {
	user := getEnv("ISUBID_DB_USER", "isucon")
	pass := getEnv("ISUBID_DB_PASSWORD", "isucon")
	host := getEnv("ISUBID_DB_HOST", "127.0.0.1")
	port := getEnv("ISUBID_DB_PORT", "3306")
	name := getEnv("ISUBID_DB_NAME", "isubid")
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=UTC&multiStatements=%t",
		user, pass, host, port, name, multiStatements)
}

func connectDB() (*sqlx.DB, error) {
	db, err := sqlx.Open("mysql", dbDSN(false))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

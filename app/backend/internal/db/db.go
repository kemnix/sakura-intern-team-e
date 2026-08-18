package db

import (
	"database/sql"
	"log"
	"os"
	"strconv"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// New は DATABASE_URL に接続済みの *sql.DB を返す。
// コネクションプールのサイズは DB_MAX_OPEN_CONNS / DB_MAX_IDLE_CONNS
// 環境変数で調整でき、未設定ならそれぞれ 10 / 5 を使う。
func New() *sql.DB {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatalf("DATABASE_URL is not set")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}

	// 既定値はあくまで出発点であり、最適値は DB 側の max_connections や
	// VM のスペック、DB.Stats() の WaitCount / WaitDuration の実測に依存する。
	// そのため環境変数で調整可能にしている。
	db.SetMaxOpenConns(envInt("DB_MAX_OPEN_CONNS", 10))
	db.SetMaxIdleConns(envInt("DB_MAX_IDLE_CONNS", 5))
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(2 * time.Minute)

	for i := 0; i < 10; i++ {
		if err = db.Ping(); err == nil {
			break
		}
		log.Printf("waiting for db... (%d/10)", i+1)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Fatalf("db ping: %v", err)
	}

	log.Println("database connected")
	return db
}

// envInt は環境変数 key を正の整数として読み取り、その値を返す。
// 未設定・解析失敗・0 以下のいずれの場合も fallback を返し、
// 不正な値でプロセスを終了させることはない。
func envInt(key string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

package main

import (
	"context"

	"github.com/cohive-ai/cohive/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openDB(ctx context.Context) (*pgxpool.Pool, error) {
	url, err := databaseURL()
	if err != nil {
		return nil, err
	}
	return db.Open(ctx, url)
}

// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.25.0

package database

import (
	"database/sql"
	"time"
)

type Container struct {
	ID      int64
	Name    sql.NullString
	LocalID sql.NullString
	Image   sql.NullString
	Status  sql.NullString
}

type Flow struct {
	ID          int64
	CreatedAt   sql.NullTime
	UpdatedAt   sql.NullTime
	Name        sql.NullString
	Status      sql.NullString
	ContainerID sql.NullInt64
}

type Log struct {
	ID        int64
	Message   string
	CreatedAt time.Time
	FlowID    sql.NullInt64
	Type      string
}

type Task struct {
	ID         int64
	CreatedAt  sql.NullTime
	UpdatedAt  sql.NullTime
	Type       sql.NullString
	Status     sql.NullString
	Args       sql.NullString
	Results    sql.NullString
	Message    sql.NullString
	FlowID     sql.NullInt64
	ToolCallID sql.NullString
}

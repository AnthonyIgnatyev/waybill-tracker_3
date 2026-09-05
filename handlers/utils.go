package handlers

import (
	"database/sql"
	"log"
	"strconv"
)

// formatNullFloat — форматирует NULL-безопасное число: "7.5" или "не указан".
func formatNullFloat(v sql.NullFloat64) string {
	if !v.Valid {
		return "не указан"
	}
	return strconv.FormatFloat(v.Float64, 'f', 1, 64)
}

// boolInt — true -> 1, false -> 0 (для флагов в SQLite).
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// parseOptFloat — число из формы или NULL для БД.
func parseOptFloat(s string) interface{} {
	if s == "" {
		return nil
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v
	}
	return nil
}

// parseOptInt — число из формы или значение по умолчанию.
func parseOptInt(s string, def int) int {
	if s == "" {
		return def
	}
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}

// execSQL — безопасное выполнение INSERT/UPDATE/DELETE с логированием ошибок.
// Возвращает ошибку, чтобы хендлер мог корректно отреагировать.
func execSQL(query string, args ...interface{}) error {
	res, err := DB.Exec(query, args...)
	if err != nil {
		log.Printf("❌ SQL Exec error: %v | query: %s | args: %v", err, query, args)
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		log.Printf("⚠️ SQL Exec не затронул строк: %s | args: %v", query, args)
	}
	return nil
}

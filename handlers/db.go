package handlers

import (
	"database/sql"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

func InitDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size=-20000",
	}

	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return nil, err
		}
	}

	if err := createSchema(db); err != nil {
		return nil, err
	}

	if err := migrate(db); err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	DB = db
	return db, nil
}

func migrate(db *sql.DB) error {
	// waybills.no_fuel_reason
	var hasReason int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('waybills')
		WHERE name = 'no_fuel_reason'`).Scan(&hasReason); err != nil {
		return err
	}
	if hasReason == 0 {
		if _, err := db.Exec(`ALTER TABLE waybills ADD COLUMN no_fuel_reason TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
		log.Println("✅ Миграция: добавлена колонка waybills.no_fuel_reason")
	}

	// drivers.license_number
	var hasLicense int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('drivers')
		WHERE name = 'license_number'`).Scan(&hasLicense); err != nil {
		return err
	}
	if hasLicense == 0 {
		if _, err := db.Exec(`ALTER TABLE drivers ADD COLUMN license_number TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
		log.Println("✅ Миграция: добавлена колонка drivers.license_number")
	}

	// maintenance_logs.attachment
	var hasAttach int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('maintenance_logs')
		WHERE name = 'attachment'`).Scan(&hasAttach); err != nil {
		return err
	}
	if hasAttach == 0 {
		if _, err := db.Exec(`ALTER TABLE maintenance_logs ADD COLUMN attachment TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
		log.Println("✅ Миграция: добавлена колонка maintenance_logs.attachment")
	}

	return nil
}

func createSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS drivers (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	last_name TEXT NOT NULL,
	license_number TEXT NOT NULL DEFAULT '',
	is_fired INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS vehicles (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	license_plate TEXT NOT NULL UNIQUE,
	brand_model TEXT,
	avg_consumption REAL,
	current_mileage INTEGER NOT NULL DEFAULT 0,
	tank_capacity REAL,
	oil_interval INTEGER NOT NULL DEFAULT 7000,
	is_deleted INTEGER NOT NULL DEFAULT 0,
	deletion_reason TEXT,
	deletion_date TEXT
);

CREATE TABLE IF NOT EXISTS waybills (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	waybill_number TEXT NOT NULL UNIQUE,
	vehicle_id INTEGER NOT NULL REFERENCES vehicles(id),
	driver_id INTEGER NOT NULL REFERENCES drivers(id),
	open_date TEXT NOT NULL,
	close_date TEXT NOT NULL,
	start_mileage INTEGER NOT NULL,
	end_mileage INTEGER NOT NULL,
	fuel_start REAL,
	fuel_end REAL,
	fuel_added REAL NOT NULL DEFAULT 0,
	no_fuel_flag INTEGER NOT NULL DEFAULT 0,
	over_limit_flag INTEGER NOT NULL DEFAULT 0,
	mileage_mismatch INTEGER NOT NULL DEFAULT 0,
	no_fuel_reason TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_waybills_vehicle ON waybills(vehicle_id, close_date);
CREATE INDEX IF NOT EXISTS idx_waybills_number  ON waybills(vehicle_id, waybill_number);

CREATE TABLE IF NOT EXISTS maintenance_logs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	vehicle_id INTEGER NOT NULL REFERENCES vehicles(id),
	act_number TEXT NOT NULL,
	service_date TEXT NOT NULL,
	mileage INTEGER NOT NULL,
	service_name TEXT DEFAULT '',
	cost REAL DEFAULT 0,
	oil_changed INTEGER NOT NULL DEFAULT 0,
	oil_filter_changed INTEGER NOT NULL DEFAULT 0,
	air_filter_changed INTEGER NOT NULL DEFAULT 0,
	front_pads_changed INTEGER NOT NULL DEFAULT 0,
	rear_pads_changed INTEGER NOT NULL DEFAULT 0,
	spark_plugs_changed INTEGER NOT NULL DEFAULT 0,
	timing_belt_changed INTEGER NOT NULL DEFAULT 0,
	timing_chain_changed INTEGER NOT NULL DEFAULT 0,
	other_work TEXT,
	other_remind_km INTEGER NOT NULL DEFAULT 0,
	attachment TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_maint_vehicle ON maintenance_logs(vehicle_id, mileage);
CREATE INDEX IF NOT EXISTS idx_maint_vehicle_date ON maintenance_logs(vehicle_id, service_date, cost);

CREATE TABLE IF NOT EXISTS vehicle_number_history (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	vehicle_license_plate TEXT NOT NULL REFERENCES vehicles(license_plate),
	old_number TEXT NOT NULL,
	new_number TEXT NOT NULL,
	change_date TEXT NOT NULL,
	comment TEXT
);`)

	if err == nil {
		log.Println("✅ Схема БД готова")
	}

	return err
}

package handlers

import (
	"log"
	"net/http"
	"strconv"
	"time"
)

type VehicleReport struct {
	LicensePlate, BrandModel                                     string
	TotalMileage, TripsCount                                     int
	TotalFuel, TotalMaintenance, AvgConsumption, NormConsumption float64
}

type DriverReport struct {
	DriverName                                        string
	TripsCount, TotalMileage, NormalTrips, IssueTrips int
	IssueRate                                         float64
}

type PeriodStats struct {
	TotalMileage, TotalTrips    int
	TotalFuel, TotalMaintenance float64
}

// ReportsHandler — отчёты по автомобилям и водителям.
func ReportsHandler(w http.ResponseWriter, r *http.Request) {
	now := time.Now()

	year := parseOptInt(r.FormValue("year"), now.Year())
	month := parseOptInt(r.FormValue("month"), int(now.Month()))

	if month < 1 || month > 12 {
		month = int(now.Month())
	}

	activeTab := r.FormValue("tab")
	if activeTab == "" {
		activeTab = "vehicles"
	}

	allTime := r.FormValue("all_time") == "on"

	var startDate, endDate string
	if allTime {
		startDate = "2000-01-01"
		endDate = "2099-12-31"
	} else {
		first := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		startDate = first.Format("2006-01-02")
		endDate = first.AddDate(0, 1, -1).Format("2006-01-02")
	}

	var stats PeriodStats

	err := DB.QueryRow(`
        SELECT COALESCE(SUM(end_mileage - start_mileage),0),
               COALESCE(SUM(fuel_added),0),
               COUNT(*)
        FROM waybills
        WHERE open_date >= ? AND close_date <= ?`,
		startDate,
		endDate,
	).Scan(&stats.TotalMileage, &stats.TotalFuel, &stats.TotalTrips)
	if err != nil {
		log.Printf("❌ reports stats: %v", err)
	}

	err = DB.QueryRow(`
        SELECT COALESCE(SUM(cost),0)
        FROM maintenance_logs
        WHERE service_date >= ? AND service_date <= ?`,
		startDate,
		endDate,
	).Scan(&stats.TotalMaintenance)
	if err != nil {
		log.Printf("❌ reports maintenance cost: %v", err)
	}

	// --- Автомобили ---
	vRows, err := DB.Query(`
        SELECT v.license_plate,
               v.brand_model,
               COALESCE(SUM(w.end_mileage - w.start_mileage),0),
               COALESCE(SUM(w.fuel_added),0),
               COUNT(w.id),
               COALESCE(v.avg_consumption,0),
               COALESCE((
                   SELECT SUM(m.cost)
                   FROM maintenance_logs m
                   WHERE m.vehicle_id=v.id
                     AND m.service_date >= ?
                     AND m.service_date <= ?
               ),0)
        FROM vehicles v
        LEFT JOIN waybills w ON w.vehicle_id=v.id AND w.open_date >= ? AND w.close_date <= ?
        WHERE v.is_deleted = 0
        GROUP BY v.id
        ORDER BY 3 DESC`,
		startDate,
		endDate,
		startDate,
		endDate,
	)

	var vReps []VehicleReport

	if err == nil {
		defer vRows.Close()

		for vRows.Next() {
			var vr VehicleReport

			if err := vRows.Scan(
				&vr.LicensePlate,
				&vr.BrandModel,
				&vr.TotalMileage,
				&vr.TotalFuel,
				&vr.TripsCount,
				&vr.NormConsumption,
				&vr.TotalMaintenance,
			); err != nil {
				log.Printf("❌ reports vehicle Scan: %v", err)
				continue
			}

			if vr.TripsCount == 0 && vr.TotalMaintenance == 0 {
				continue
			}

			if vr.TotalMileage > 0 {
				vr.AvgConsumption = vr.TotalFuel / float64(vr.TotalMileage) * 100
			}

			vReps = append(vReps, vr)
		}

		if err := vRows.Err(); err != nil {
			log.Printf("❌ reports vehicle rows.Err: %v", err)
		}
	} else {
		log.Printf("❌ reports vehicles: %v", err)
	}

	// --- Водители ---
	dRows, err := DB.Query(`
        SELECT d.last_name,
               COUNT(w.id),
               COALESCE(SUM(w.end_mileage - w.start_mileage),0),
               COALESCE(SUM(CASE WHEN w.id IS NOT NULL AND w.mileage_mismatch=0 AND w.no_fuel_flag=0 THEN 1 ELSE 0 END),0),
               COALESCE(SUM(CASE WHEN w.mileage_mismatch=1 OR w.no_fuel_flag=1 THEN 1 ELSE 0 END),0)
        FROM drivers d
        LEFT JOIN waybills w ON w.driver_id=d.id AND w.open_date >= ? AND w.close_date <= ?
        WHERE d.is_fired = 0
        GROUP BY d.id
        ORDER BY 3 DESC`,
		startDate,
		endDate,
	)

	var dReps []DriverReport

	if err == nil {
		defer dRows.Close()

		for dRows.Next() {
			var dr DriverReport

			if err := dRows.Scan(
				&dr.DriverName,
				&dr.TripsCount,
				&dr.TotalMileage,
				&dr.NormalTrips,
				&dr.IssueTrips,
			); err != nil {
				log.Printf("❌ reports driver Scan: %v", err)
				continue
			}

			if dr.TripsCount > 0 {
				dr.IssueRate = float64(dr.IssueTrips) / float64(dr.TripsCount) * 100
			}

			dReps = append(dReps, dr)
		}

		if err := dRows.Err(); err != nil {
			log.Printf("❌ reports driver rows.Err: %v", err)
		}
	} else {
		log.Printf("❌ reports drivers: %v", err)
	}

	// --- Данные для фильтра ---
	months := []map[string]string{}
	names := []string{
		"Январь",
		"Февраль",
		"Март",
		"Апрель",
		"Май",
		"Июнь",
		"Июль",
		"Август",
		"Сентябрь",
		"Октябрь",
		"Ноябрь",
		"Декабрь",
	}

	for i, name := range names {
		months = append(months, map[string]string{
			"value": strconv.Itoa(i + 1),
			"name":  name,
		})
	}

	years := []int{now.Year(), now.Year() - 1, now.Year() - 2}

	renderTemplate(w, "reports.html", map[string]interface{}{
		"VehicleReports": vReps,
		"DriverReports":  dReps,
		"MonthlyStats":   stats,
		"ActiveTab":      activeTab,
		"Year":           year,
		"Month":          month,
		"Months":         months,
		"Years":          years,
		"AllTime":        allTime,
	})
}

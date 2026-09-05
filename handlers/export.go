package handlers

import (
	"encoding/csv"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// beginCSV устанавливает заголовки и пишет BOM для корректного открытия в Excel.
func beginCSV(w http.ResponseWriter, filename string) *csv.Writer {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))

	// UTF-8 BOM, чтобы Excel правильно отображал кириллицу
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})

	writer := csv.NewWriter(w)
	return writer
}

// ---------- Экспорт путевых листов ----------

func ExportWaybillsCSV(w http.ResponseWriter, r *http.Request) {
	startDate := r.FormValue("start_date")
	endDate := r.FormValue("end_date")

	if _, ok := parseDateValue(startDate); !ok {
		startDate = ""
	}
	if _, ok := parseDateValue(endDate); !ok {
		endDate = ""
	}
	if startDate != "" && endDate != "" && endDate < startDate {
		endDate = startDate
	}

	search := strings.TrimSpace(r.FormValue("search"))
	vehicleFilter := r.FormValue("vehicle_id")
	vehicleIDFilter := parseOptInt(vehicleFilter, 0)

	where := " WHERE 1=1"
	var args []interface{}

	if startDate != "" {
		where += " AND w.open_date >= ?"
		args = append(args, startDate)
	}
	if endDate != "" {
		where += " AND w.close_date <= ?"
		args = append(args, endDate)
	}
	if vehicleIDFilter > 0 {
		where += " AND w.vehicle_id = ?"
		args = append(args, vehicleIDFilter)
	}
	if search != "" {
		where += " AND w.waybill_number LIKE '%' || ? || '%'"
		args = append(args, search)
	}

	query := waybillSelect + where + " ORDER BY w.close_date DESC, w.id DESC"

	rows, err := DB.Query(query, args...)
	if err != nil {
		log.Printf("❌ ExportWaybillsCSV Query: %v", err)
		http.Error(w, "Ошибка БД", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	filename := "putevye_listy.csv"
	if startDate != "" && endDate != "" {
		filename = fmt.Sprintf("putevye_listy_%s_%s.csv", startDate, endDate)
	} else if startDate != "" {
		filename = fmt.Sprintf("putevye_listy_ot_%s.csv", startDate)
	} else if endDate != "" {
		filename = fmt.Sprintf("putevye_listy_do_%s.csv", endDate)
	}

	writer := beginCSV(w, filename)
	defer writer.Flush()

	_ = writer.Write([]string{
		"№ листа",
		"Автомобиль",
		"Водитель",
		"Дата открытия",
		"Дата закрытия",
		"Начальный пробег (км)",
		"Конечный пробег (км)",
		"Пробег за смену (км)",
		"Топливо при выезде (л)",
		"Топливо при возврате (л)",
		"Заправлено (л)",
		"Расход (л/100км)",
		"Без заправки",
		"Причина отсутствия заправки",
		"Разрыв пробега",
		"Перерасход",
	})

	for rows.Next() {
		var row waybillRow

		if err := rows.Scan(
			&row.ID,
			&row.WaybillNumber,
			&row.OpenDate,
			&row.CloseDate,
			&row.StartMileage,
			&row.EndMileage,
			&row.Mileage,
			&row.DriverName,
			&row.LicensePlate,
			&row.AvgConsumption,
			&row.TankCapacity,
			&row.FuelAdded,
			&row.FuelStart,
			&row.FuelEnd,
			&row.MileageMismatch,
			&row.NoFuelFlag,
			&row.NoFuelReason,
			&row.PrevEndMileage,
		); err != nil {
			log.Printf("❌ ExportWaybillsCSV Scan: %v", err)
			continue
		}

		if row.Mileage > 0 {
			if row.FuelStart > 0 || row.FuelEnd > 0 {
				if actual := row.FuelStart + row.FuelAdded - row.FuelEnd; actual > 0 {
					row.ConsumptionPer100 = actual / float64(row.Mileage) * 100
				}
			} else {
				row.ConsumptionPer100 = row.FuelAdded / float64(row.Mileage) * 100
			}
		}

		if row.AvgConsumption > 0 && row.ConsumptionPer100-row.AvgConsumption > 2 {
			row.Overconsumption = true
		}

		consumptionStr := ""
		if row.ConsumptionPer100 > 0 {
			consumptionStr = strconv.FormatFloat(row.ConsumptionPer100, 'f', 1, 64)
		}

		fuelStartStr := ""
		if row.FuelStart > 0 {
			fuelStartStr = strconv.FormatFloat(row.FuelStart, 'f', 1, 64)
		}

		fuelEndStr := ""
		if row.FuelEnd > 0 {
			fuelEndStr = strconv.FormatFloat(row.FuelEnd, 'f', 1, 64)
		}

		noFuel := ""
		if row.NoFuelFlag {
			noFuel = "Да"
		}

		mismatch := ""
		if row.MileageMismatch {
			mismatch = "Да"
		}

		overconsumption := ""
		if row.Overconsumption {
			overconsumption = "Да"
		}

		_ = writer.Write([]string{
			row.WaybillNumber,
			row.LicensePlate,
			row.DriverName,
			row.OpenDate,
			row.CloseDate,
			strconv.Itoa(row.StartMileage),
			strconv.Itoa(row.EndMileage),
			strconv.Itoa(row.Mileage),
			fuelStartStr,
			fuelEndStr,
			strconv.FormatFloat(row.FuelAdded, 'f', 1, 64),
			consumptionStr,
			noFuel,
			row.NoFuelReason,
			mismatch,
			overconsumption,
		})
	}

	if err := rows.Err(); err != nil {
		log.Printf("❌ ExportWaybillsCSV rows.Err: %v", err)
	}
}

// ---------- Экспорт отчёта по автомобилям ----------

func ExportVehiclesReportCSV(w http.ResponseWriter, r *http.Request) {
	now := time.Now()

	year := parseOptInt(r.FormValue("year"), now.Year())
	month := parseOptInt(r.FormValue("month"), int(now.Month()))
	if month < 1 || month > 12 {
		month = int(now.Month())
	}

	allTime := r.FormValue("all_time") == "on"

	var startDate, endDate string
	if allTime {
		startDate, endDate = "2000-01-01", "2099-12-31"
	} else {
		first := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		startDate = first.Format("2006-01-02")
		endDate = first.AddDate(0, 1, -1).Format("2006-01-02")
	}

	rows, err := DB.Query(`
		SELECT v.license_plate, v.brand_model,
		       COALESCE(SUM(w.end_mileage - w.start_mileage),0),
		       COALESCE(SUM(w.fuel_added),0),
		       COUNT(w.id),
		       COALESCE(v.avg_consumption,0),
		       COALESCE((SELECT SUM(m.cost) FROM maintenance_logs m
		                 WHERE m.vehicle_id=v.id AND m.service_date >= ? AND m.service_date <= ?),0)
		FROM vehicles v
		LEFT JOIN waybills w ON w.vehicle_id=v.id AND w.open_date >= ? AND w.close_date <= ?
		WHERE v.is_deleted = 0
		GROUP BY v.id
		ORDER BY 3 DESC`, startDate, endDate, startDate, endDate)
	if err != nil {
		log.Printf("❌ ExportVehiclesReportCSV Query: %v", err)
		http.Error(w, "Ошибка БД", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	filename := fmt.Sprintf("otchet_avto_%d_%02d.csv", year, month)
	if allTime {
		filename = "otchet_avto_vse_vremya.csv"
	}

	writer := beginCSV(w, filename)
	defer writer.Flush()

	_ = writer.Write([]string{
		"Госномер",
		"Марка / Модель",
		"Путевых листов",
		"Пробег (км)",
		"Топливо (л)",
		"Средний расход (л/100км)",
		"Норма расхода (л/100км)",
		"Затраты на ТО (₽)",
	})

	for rows.Next() {
		var vr VehicleReport

		if err := rows.Scan(
			&vr.LicensePlate, &vr.BrandModel, &vr.TotalMileage, &vr.TotalFuel,
			&vr.TripsCount, &vr.NormConsumption, &vr.TotalMaintenance,
		); err != nil {
			log.Printf("❌ ExportVehiclesReportCSV Scan: %v", err)
			continue
		}

		if vr.TripsCount == 0 && vr.TotalMaintenance == 0 {
			continue
		}

		avgConsumption := 0.0
		if vr.TotalMileage > 0 {
			avgConsumption = vr.TotalFuel / float64(vr.TotalMileage) * 100
		}

		_ = writer.Write([]string{
			vr.LicensePlate,
			vr.BrandModel,
			strconv.Itoa(vr.TripsCount),
			strconv.Itoa(vr.TotalMileage),
			strconv.FormatFloat(vr.TotalFuel, 'f', 1, 64),
			strconv.FormatFloat(avgConsumption, 'f', 1, 64),
			strconv.FormatFloat(vr.NormConsumption, 'f', 1, 64),
			strconv.FormatFloat(vr.TotalMaintenance, 'f', 2, 64),
		})
	}

	if err := rows.Err(); err != nil {
		log.Printf("❌ ExportVehiclesReportCSV rows.Err: %v", err)
	}
}

// ---------- Экспорт отчёта по водителям ----------

func ExportDriversReportCSV(w http.ResponseWriter, r *http.Request) {
	now := time.Now()

	year := parseOptInt(r.FormValue("year"), now.Year())
	month := parseOptInt(r.FormValue("month"), int(now.Month()))
	if month < 1 || month > 12 {
		month = int(now.Month())
	}

	allTime := r.FormValue("all_time") == "on"

	var startDate, endDate string
	if allTime {
		startDate, endDate = "2000-01-01", "2099-12-31"
	} else {
		first := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		startDate = first.Format("2006-01-02")
		endDate = first.AddDate(0, 1, -1).Format("2006-01-02")
	}

	rows, err := DB.Query(`
		SELECT d.last_name,
		       COUNT(w.id),
		       COALESCE(SUM(w.end_mileage - w.start_mileage),0),
		       COALESCE(SUM(CASE WHEN w.id IS NOT NULL AND w.mileage_mismatch=0 AND w.no_fuel_flag=0 THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN w.mileage_mismatch=1 OR w.no_fuel_flag=1 THEN 1 ELSE 0 END),0)
		FROM drivers d
		LEFT JOIN waybills w ON w.driver_id=d.id AND w.open_date >= ? AND w.close_date <= ?
		WHERE d.is_fired = 0
		GROUP BY d.id
		ORDER BY 3 DESC`, startDate, endDate)
	if err != nil {
		log.Printf("❌ ExportDriversReportCSV Query: %v", err)
		http.Error(w, "Ошибка БД", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	filename := fmt.Sprintf("otchet_voditeli_%d_%02d.csv", year, month)
	if allTime {
		filename = "otchet_voditeli_vse_vremya.csv"
	}

	writer := beginCSV(w, filename)
	defer writer.Flush()

	_ = writer.Write([]string{
		"Водитель",
		"Смен отработано",
		"Общий пробег (км)",
		"Без замечаний",
		"С замечаниями",
		"% замечаний",
	})

	for rows.Next() {
		var dr DriverReport

		if err := rows.Scan(
			&dr.DriverName, &dr.TripsCount, &dr.TotalMileage,
			&dr.NormalTrips, &dr.IssueTrips,
		); err != nil {
			log.Printf("❌ ExportDriversReportCSV Scan: %v", err)
			continue
		}

		issueRate := 0.0
		if dr.TripsCount > 0 {
			issueRate = float64(dr.IssueTrips) / float64(dr.TripsCount) * 100
		}

		_ = writer.Write([]string{
			dr.DriverName,
			strconv.Itoa(dr.TripsCount),
			strconv.Itoa(dr.TotalMileage),
			strconv.Itoa(dr.NormalTrips),
			strconv.Itoa(dr.IssueTrips),
			strconv.FormatFloat(issueRate, 'f', 1, 64),
		})
	}

	if err := rows.Err(); err != nil {
		log.Printf("❌ ExportDriversReportCSV rows.Err: %v", err)
	}
}

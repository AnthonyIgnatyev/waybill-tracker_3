package handlers

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
)

type waybillRow struct {
	ID, StartMileage, EndMileage, Mileage, PrevEndMileage                          int
	WaybillNumber, OpenDate, CloseDate, DriverName, LicensePlate, Notes            string
	NoFuelReason                                                                   string
	AvgConsumption, TankCapacity, FuelAdded, FuelStart, FuelEnd, ConsumptionPer100 float64
	MileageMismatch, NoFuelFlag, Overconsumption                                   bool
}

const waybillSelect = `
SELECT w.id,
       w.waybill_number,
       strftime('%d.%m.%Y', w.open_date),
       strftime('%d.%m.%Y', w.close_date),
       w.start_mileage,
       w.end_mileage,
       w.end_mileage - w.start_mileage,
       d.last_name,
       v.license_plate,
       COALESCE(v.avg_consumption,0),
       COALESCE(v.tank_capacity,0),
       COALESCE(w.fuel_added,0),
       COALESCE(w.fuel_start,0),
       COALESCE(w.fuel_end,0),
       w.mileage_mismatch,
       w.no_fuel_flag,
       COALESCE(w.no_fuel_reason,''),
       COALESCE(
           (SELECT w2.end_mileage
            FROM waybills w2
            WHERE w2.vehicle_id = w.vehicle_id
              AND CAST(w2.waybill_number AS INTEGER) < CAST(w.waybill_number AS INTEGER)
            ORDER BY CAST(w2.waybill_number AS INTEGER) DESC
            LIMIT 1),
           0
       )
FROM waybills w
JOIN drivers d ON d.id = w.driver_id
JOIN vehicles v ON v.id = w.vehicle_id`

func normalizeWaybillSort(sort string) (string, string) {
	switch sort {
	case "date_asc":
		return "date_asc", "w.close_date ASC, w.id ASC"
	case "number_desc":
		return "number_desc", "CAST(w.waybill_number AS INTEGER) DESC, w.id DESC"
	case "number_asc":
		return "number_asc", "CAST(w.waybill_number AS INTEGER) ASC, w.id ASC"
	case "mileage_desc":
		return "mileage_desc", "(w.end_mileage - w.start_mileage) DESC, w.id DESC"
	case "mileage_asc":
		return "mileage_asc", "(w.end_mileage - w.start_mileage) ASC, w.id ASC"
	default:
		return "date_desc", "w.close_date DESC, w.id DESC"
	}
}

func renderAddWaybillPage(w http.ResponseWriter, errs []string, form WaybillForm) {
	renderTemplate(w, "add_waybill.html", map[string]interface{}{
		"Errors":   errs,
		"Form":     form,
		"Drivers":  activeDrivers(),
		"Vehicles": activeVehicles(),
	})
}

func renderEditWaybillPage(w http.ResponseWriter, errs []string, form WaybillForm) {
	renderTemplate(w, "edit_waybill.html", map[string]interface{}{
		"Errors":   errs,
		"Form":     form,
		"Drivers":  activeDrivers(),
		"Vehicles": activeVehicles(),
	})
}

func WaybillsHandler(w http.ResponseWriter, r *http.Request) {
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

	var total int
	if err := DB.QueryRow("SELECT COUNT(*) FROM waybills w"+where, args...).Scan(&total); err != nil {
		log.Printf("❌ waybills count: %v", err)
		http.Error(w, "Ошибка БД", http.StatusInternalServerError)
		return
	}

	sort, order := normalizeWaybillSort(r.FormValue("sort"))
	pagination := buildPagination(r, total, 50)
	offset := (pagination.Page - 1) * pagination.PageSize

	query := waybillSelect + where + " ORDER BY " + order + " LIMIT ? OFFSET ?"

	queryArgs := make([]interface{}, 0, len(args)+2)
	queryArgs = append(queryArgs, args...)
	queryArgs = append(queryArgs, pagination.PageSize, offset)

	rows, err := DB.Query(query, queryArgs...)
	if err != nil {
		log.Printf("❌ WaybillsHandler Query: %v", err)
		http.Error(w, "Ошибка БД", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var list []waybillRow

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
			log.Printf("❌ WaybillsHandler Scan: %v", err)
			continue
		}

		// Расход считается всегда, даже если заправки не было
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

		var notes []string

		if row.MileageMismatch {
			if gap := row.StartMileage - row.PrevEndMileage; gap > 0 {
				notes = append(notes, "Разрыв пробега: не учтено "+strconv.Itoa(gap)+" км")
			} else {
				notes = append(notes, "Разрыв пробега")
			}
		}

		if row.NoFuelFlag {
			note := "Без заправки"
			if row.NoFuelReason != "" {
				note += ". Причина: " + row.NoFuelReason
			}
			notes = append(notes, note)
		}

		if row.Overconsumption {
			notes = append(notes, "Перерасход. Норма: "+
				strconv.FormatFloat(row.AvgConsumption, 'f', 1, 64)+" л/100км")
		}

		if row.TankCapacity > 0 && row.FuelEnd > 0 && row.FuelEnd < row.TankCapacity-2 {
			notes = append(notes, "Неполный бак: "+
				strconv.FormatFloat(row.FuelEnd, 'f', 1, 64)+" л из "+
				strconv.FormatFloat(row.TankCapacity, 'f', 1, 64)+" л")
		}

		row.Notes = strings.Join(notes, "; ")
		list = append(list, row)
	}

	if err := rows.Err(); err != nil {
		log.Printf("❌ WaybillsHandler rows.Err: %v", err)
	}

	renderTemplate(w, "waybills.html", map[string]interface{}{
		"Waybills":      list,
		"Vehicles":      activeVehicles(),
		"StartDate":     startDate,
		"EndDate":       endDate,
		"VehicleFilter": vehicleFilter,
		"Search":        search,
		"Sort":          sort,
		"Pagination":    pagination,
	})
}

type idName struct {
	ID   int
	Name string
}

func activeVehicles() []idName {
	rows, err := DB.Query(`
		SELECT id, license_plate
		FROM vehicles
		WHERE is_deleted = 0
		ORDER BY license_plate`)
	if err != nil {
		log.Printf("❌ activeVehicles Query: %v", err)
		return nil
	}
	defer rows.Close()

	var list []idName

	for rows.Next() {
		var v idName
		if err := rows.Scan(&v.ID, &v.Name); err != nil {
			log.Printf("❌ activeVehicles Scan: %v", err)
			continue
		}
		list = append(list, v)
	}

	if err := rows.Err(); err != nil {
		log.Printf("❌ activeVehicles rows.Err: %v", err)
	}

	return list
}

func activeDrivers() []idName {
	rows, err := DB.Query(`
		SELECT id, last_name
		FROM drivers
		WHERE is_fired = 0
		ORDER BY last_name`)
	if err != nil {
		log.Printf("❌ activeDrivers Query: %v", err)
		return nil
	}
	defer rows.Close()

	var list []idName

	for rows.Next() {
		var d idName
		if err := rows.Scan(&d.ID, &d.Name); err != nil {
			log.Printf("❌ activeDrivers Scan: %v", err)
			continue
		}
		list = append(list, d)
	}

	if err := rows.Err(); err != nil {
		log.Printf("❌ activeDrivers rows.Err: %v", err)
	}

	return list
}

func AddWaybillHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		renderAddWaybillPage(w, nil, WaybillForm{})
		return
	}

	if err := r.ParseForm(); err != nil {
		renderAddWaybillPage(w, []string{"Ошибка данных формы."}, WaybillForm{})
		return
	}

	form := waybillFormFromRequest(r)

	data, errs := validateWaybillForm(form, 0)
	if len(errs) > 0 {
		renderAddWaybillPage(w, errs, form)
		return
	}

	prev := prevEndMileage(data.VehicleID, data.Number)
	mismatch := prev >= 0 && prev != data.StartMileage

	err := execSQL(`
		INSERT INTO waybills (
			waybill_number,
			vehicle_id,
			driver_id,
			open_date,
			close_date,
			start_mileage,
			end_mileage,
			fuel_start,
			fuel_end,
			fuel_added,
			no_fuel_flag,
			over_limit_flag,
			mileage_mismatch,
			no_fuel_reason
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		data.Number,
		data.VehicleID,
		data.DriverID,
		data.OpenDate,
		data.CloseDate,
		data.StartMileage,
		data.EndMileage,
		data.FuelStart,
		data.FuelEnd,
		data.FuelAdded,
		boolInt(data.NoFuel),
		0,
		boolInt(mismatch),
		data.NoFuelReason,
	)
	if err != nil {
		renderAddWaybillPage(w, []string{
			"Не удалось сохранить путевой лист. Проверьте данные и попробуйте ещё раз.",
		}, form)
		return
	}

	if err := RecalculateMileageMismatch(data.VehicleID); err != nil {
		log.Printf("⚠️ RecalculateMileageMismatch: %v", err)
	}

	updateCurrentMileage(data.VehicleID)

	http.Redirect(w, r, fmt.Sprintf("/waybills?vehicle_id=%d", data.VehicleID), http.StatusSeeOther)
}

func EditWaybillHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		idStr := r.URL.Query().Get("id")

		id, ok := parseIntValue(idStr)
		if !ok || id <= 0 {
			http.Error(w, "Не указан ID путевого листа", http.StatusBadRequest)
			return
		}

		var wb struct {
			ID, VehicleID, DriverID, StartMileage, EndMileage int
			WaybillNumber, OpenDate, CloseDate, NoFuelReason  string
			FuelStart, FuelEnd                                sql.NullFloat64
			FuelAdded                                         float64
			NoFuelFlag, OverLimitFlag                         bool
		}

		err := DB.QueryRow(`
			SELECT id, waybill_number, vehicle_id, driver_id, open_date, close_date,
			       start_mileage, end_mileage, fuel_start, fuel_end, fuel_added,
			       no_fuel_flag, over_limit_flag, COALESCE(no_fuel_reason,'')
			FROM waybills
			WHERE id = ?`, id).Scan(
			&wb.ID,
			&wb.WaybillNumber,
			&wb.VehicleID,
			&wb.DriverID,
			&wb.OpenDate,
			&wb.CloseDate,
			&wb.StartMileage,
			&wb.EndMileage,
			&wb.FuelStart,
			&wb.FuelEnd,
			&wb.FuelAdded,
			&wb.NoFuelFlag,
			&wb.OverLimitFlag,
			&wb.NoFuelReason,
		)
		if err != nil {
			log.Printf("❌ query waybill for edit: %v", err)
			http.Error(w, "Путевой лист не найден", http.StatusNotFound)
			return
		}

		form := WaybillForm{
			ID:             strconv.Itoa(wb.ID),
			WaybillNumber:  wb.WaybillNumber,
			VehicleID:      strconv.Itoa(wb.VehicleID),
			DriverID:       strconv.Itoa(wb.DriverID),
			OpenDate:       wb.OpenDate,
			CloseDate:      wb.CloseDate,
			StartMileage:   strconv.Itoa(wb.StartMileage),
			EndMileage:     strconv.Itoa(wb.EndMileage),
			FuelStart:      nullFloatToString(wb.FuelStart),
			FuelEnd:        nullFloatToString(wb.FuelEnd),
			FuelAdded:      strconv.FormatFloat(wb.FuelAdded, 'f', -1, 64),
			NoFuelReasonOn: wb.NoFuelReason != "",
			NoFuelReason:   wb.NoFuelReason,
		}

		renderEditWaybillPage(w, nil, form)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ошибка формы", http.StatusBadRequest)
		return
	}

	form := waybillFormFromRequest(r)

	id, ok := parseIntValue(form.ID)
	if !ok || id <= 0 {
		http.Error(w, "Не указан ID путевого листа", http.StatusBadRequest)
		return
	}

	data, errs := validateWaybillForm(form, id)
	if len(errs) > 0 {
		renderEditWaybillPage(w, errs, form)
		return
	}

	prev := prevEndMileage(data.VehicleID, data.Number)
	mismatch := prev >= 0 && prev != data.StartMileage

	err := execSQL(`
		UPDATE waybills
		SET waybill_number=?, vehicle_id=?, driver_id=?, open_date=?, close_date=?,
			start_mileage=?, end_mileage=?, fuel_start=?, fuel_end=?, fuel_added=?,
			no_fuel_flag=?, over_limit_flag=?, mileage_mismatch=?, no_fuel_reason=?
		WHERE id=?`,
		data.Number,
		data.VehicleID,
		data.DriverID,
		data.OpenDate,
		data.CloseDate,
		data.StartMileage,
		data.EndMileage,
		data.FuelStart,
		data.FuelEnd,
		data.FuelAdded,
		boolInt(data.NoFuel),
		0,
		boolInt(mismatch),
		data.NoFuelReason,
		id,
	)
	if err != nil {
		renderEditWaybillPage(w, []string{
			"Не удалось сохранить изменения. Проверьте данные и попробуйте ещё раз.",
		}, form)
		return
	}

	if err := RecalculateMileageMismatch(data.VehicleID); err != nil {
		log.Printf("⚠️ RecalculateMileageMismatch: %v", err)
	}

	updateCurrentMileage(data.VehicleID)

	http.Redirect(w, r, fmt.Sprintf("/waybills?vehicle_id=%d", data.VehicleID), http.StatusSeeOther)
}

func prevEndMileage(vehicleId int, waybillNumber string) int {
	var prev sql.NullInt64

	err := DB.QueryRow(`
		SELECT end_mileage
		FROM waybills
		WHERE vehicle_id = ?
		  AND CAST(waybill_number AS INTEGER) < CAST(? AS INTEGER)
		ORDER BY CAST(waybill_number AS INTEGER) DESC
		LIMIT 1`,
		vehicleId,
		waybillNumber,
	).Scan(&prev)

	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("⚠️ prevEndMileage: %v", err)
		}
		return -1
	}

	if !prev.Valid {
		return -1
	}

	return int(prev.Int64)
}

func RecalculateMileageMismatch(vehicleId int) error {
	rows, err := DB.Query(`
		SELECT id, start_mileage, end_mileage
		FROM waybills
		WHERE vehicle_id = ?
		ORDER BY CAST(waybill_number AS INTEGER)`, vehicleId)
	if err != nil {
		log.Printf("❌ RecalculateMileageMismatch Query: %v", err)
		return err
	}
	defer rows.Close()

	type wb struct {
		id    int
		start int
		end   int
	}

	var list []wb

	for rows.Next() {
		var w wb
		if err := rows.Scan(&w.id, &w.start, &w.end); err != nil {
			log.Printf("❌ RecalculateMileageMismatch Scan: %v", err)
			return err
		}
		list = append(list, w)
	}

	if err := rows.Err(); err != nil {
		log.Printf("❌ RecalculateMileageMismatch rows.Err: %v", err)
		return err
	}

	tx, err := DB.Begin()
	if err != nil {
		log.Printf("❌ RecalculateMileageMismatch Begin: %v", err)
		return err
	}
	defer tx.Rollback()

	for i := 0; i < len(list); i++ {
		mismatch := 0
		if i > 0 && list[i].start != list[i-1].end {
			mismatch = 1
		}

		if _, err := tx.Exec(
			"UPDATE waybills SET mileage_mismatch = ? WHERE id = ?",
			mismatch,
			list[i].id,
		); err != nil {
			log.Printf("❌ RecalculateMileageMismatch Exec: %v", err)
			return err
		}
	}

	return tx.Commit()
}

func updateCurrentMileage(vehicleId int) {
	var lastEnd sql.NullInt64

	err := DB.QueryRow(`
		SELECT end_mileage
		FROM waybills
		WHERE vehicle_id = ?
		ORDER BY close_date DESC, id DESC
		LIMIT 1`, vehicleId).Scan(&lastEnd)

	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("⚠️ updateCurrentMileage query: %v", err)
		}
		return
	}

	if !lastEnd.Valid {
		return
	}

	if err := execSQL(`
		UPDATE vehicles
		SET current_mileage = ?
		WHERE id = ? AND current_mileage < ?`,
		lastEnd.Int64,
		vehicleId,
		lastEnd.Int64,
	); err != nil {
		log.Printf("⚠️ updateCurrentMileage update: %v", err)
	}
}

func RecalculateAllVehicles() error {
	rows, err := DB.Query("SELECT id FROM vehicles WHERE is_deleted = 0")
	if err != nil {
		log.Printf("❌ RecalculateAllVehicles Query: %v", err)
		return err
	}
	defer rows.Close()

	var ids []int

	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			log.Printf("❌ RecalculateAllVehicles Scan: %v", err)
			continue
		}
		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		log.Printf("❌ RecalculateAllVehicles rows.Err: %v", err)
		return err
	}

	var lastErr error

	for _, id := range ids {
		if err := RecalculateMileageMismatch(id); err != nil {
			log.Printf("⚠️ RecalculateAllVehicles: vehicle %d: %v", id, err)
			lastErr = err
		}
	}

	return lastErr
}

// DeleteWaybillHandler удаляет путевой лист (безвозвратно) и откатывает пробег авто.
func DeleteWaybillHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/waybills", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ошибка формы", http.StatusBadRequest)
		return
	}

	id, ok := parseIntValue(r.FormValue("id"))
	if !ok || id <= 0 {
		http.Error(w, "Не указан ID путевого листа", http.StatusBadRequest)
		return
	}

	var vehicleID, endMileage int
	if err := DB.QueryRow(`SELECT vehicle_id, end_mileage FROM waybills WHERE id = ?`, id).
		Scan(&vehicleID, &endMileage); err != nil {
		http.Error(w, "Путевой лист не найден", http.StatusNotFound)
		return
	}

	if err := execSQL(`DELETE FROM waybills WHERE id = ?`, id); err != nil {
		http.Error(w, "Не удалось удалить путевой лист", http.StatusInternalServerError)
		return
	}

	rollbackVehicleMileage(vehicleID, endMileage)

	if err := RecalculateMileageMismatch(vehicleID); err != nil {
		log.Printf("⚠️ RecalculateMileageMismatch после удаления: %v", err)
	}

	http.Redirect(w, r, fmt.Sprintf("/waybills?vehicle_id=%d", vehicleID), http.StatusSeeOther)
}

// rollbackVehicleMileage откатывает текущий пробег автомобиля после удаления
// путевого листа. Пересчёт выполняется только если удалённый лист был источником
// максимального пробега; иначе пробег не трогаем.
func rollbackVehicleMileage(vehicleID, deletedEnd int) {
	var current int
	if err := DB.QueryRow(`SELECT current_mileage FROM vehicles WHERE id = ?`, vehicleID).Scan(&current); err != nil {
		log.Printf("⚠️ rollbackVehicleMileage: чтение пробега: %v", err)
		return
	}

	// Удалённый лист не определял максимальный пробег — откатывать нечего
	if deletedEnd < current {
		return
	}

	var maxWaybill, maxMaint sql.NullInt64
	_ = DB.QueryRow(`SELECT MAX(end_mileage) FROM waybills WHERE vehicle_id = ?`, vehicleID).Scan(&maxWaybill)
	_ = DB.QueryRow(`SELECT MAX(mileage) FROM maintenance_logs WHERE vehicle_id = ?`, vehicleID).Scan(&maxMaint)

	newCurrent := 0
	if maxWaybill.Valid {
		newCurrent = int(maxWaybill.Int64)
	}
	if maxMaint.Valid && int(maxMaint.Int64) > newCurrent {
		newCurrent = int(maxMaint.Int64)
	}

	if err := execSQL(`UPDATE vehicles SET current_mileage = ? WHERE id = ?`, newCurrent, vehicleID); err != nil {
		log.Printf("⚠️ rollbackVehicleMileage: %v", err)
	}
}

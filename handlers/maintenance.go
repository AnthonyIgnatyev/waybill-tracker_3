package handlers

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const uploadDir = "uploads"

type maintLog struct {
	ID, Mileage                                      int
	ActNumber, ServiceDate, LicensePlate, BrandModel string
	WorkDone, ServiceName, Cost, Attachment          string
}

// saveUploadedPDF читает файл из поля fieldName, проверяет, что это PDF,
// и сохраняет в uploadDir. Возвращает имя файла или "" если файл не загружен.
func saveUploadedPDF(r *http.Request, fieldName string) (string, error) {
	file, header, err := r.FormFile(fieldName)
	if err == http.ErrMissingFile {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer file.Close()

	if !strings.EqualFold(filepath.Ext(header.Filename), ".pdf") {
		return "", fmt.Errorf("допустимы только файлы PDF")
	}

	// Проверка сигнатуры %PDF
	magic := make([]byte, 5)
	n, _ := file.Read(magic)
	if n < 4 || string(magic[:4]) != "%PDF" {
		return "", fmt.Errorf("файл не является корректным PDF")
	}

	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return "", err
	}

	name := fmt.Sprintf("maint_%d.pdf", time.Now().UnixNano())
	dst, err := os.Create(filepath.Join(uploadDir, name))
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := dst.Write(magic[:n]); err != nil {
		return "", err
	}
	if _, err := io.Copy(dst, file); err != nil {
		return "", err
	}

	return name, nil
}

func deleteAttachmentFile(name string) {
	if name == "" {
		return
	}
	_ = os.Remove(filepath.Join(uploadDir, filepath.Base(name)))
}

// MaintenanceFileHandler — просмотр (inline) или скачивание PDF-акта.
func MaintenanceFileHandler(w http.ResponseWriter, r *http.Request) {
	id := parseOptInt(r.URL.Query().Get("id"), 0)
	if id <= 0 {
		http.Error(w, "Не указан ID", http.StatusBadRequest)
		return
	}

	var attachment string
	if err := DB.QueryRow(`
		SELECT COALESCE(attachment,'')
		FROM maintenance_logs WHERE id = ?`, id).Scan(&attachment); err != nil || attachment == "" {
		http.Error(w, "Файл не найден", http.StatusNotFound)
		return
	}

	base := filepath.Base(attachment)
	path := filepath.Join(uploadDir, base)

	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "Файл не найден", http.StatusNotFound)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		http.Error(w, "Файл не найден", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+base+`"`)
	} else {
		w.Header().Set("Content-Disposition", `inline; filename="`+base+`"`)
	}

	http.ServeContent(w, r, base, info.ModTime(), f)
}

func normalizeMaintenanceSort(sort string) (string, string) {
	switch sort {
	case "date_asc":
		return "date_asc", "m.service_date ASC, m.id ASC"
	case "mileage_desc":
		return "mileage_desc", "m.mileage DESC, m.id DESC"
	case "mileage_asc":
		return "mileage_asc", "m.mileage ASC, m.id ASC"
	case "cost_desc":
		return "cost_desc", "m.cost DESC, m.id DESC"
	case "cost_asc":
		return "cost_asc", "m.cost ASC, m.id ASC"
	default:
		return "date_desc", "m.service_date DESC, m.id DESC"
	}
}

func renderAddMaintenancePage(w http.ResponseWriter, errs []string, form MaintenanceForm) {
	renderTemplate(w, "add_maintenance.html", map[string]interface{}{
		"Errors":   errs,
		"Form":     form,
		"Vehicles": activeVehicles(),
	})
}

func renderEditMaintenancePage(w http.ResponseWriter, errs []string, form MaintenanceForm, maint interface{}) {
	renderTemplate(w, "edit_maintenance.html", map[string]interface{}{
		"Errors":      errs,
		"Form":        form,
		"Vehicles":    activeVehicles(),
		"Maintenance": maint,
	})
}

func MaintenanceHandler(w http.ResponseWriter, r *http.Request) {
	vehicleFilter := r.FormValue("vehicle_id")
	vehicleIDFilter := parseOptInt(vehicleFilter, 0)

	where := ""
	var args []interface{}

	if vehicleIDFilter > 0 {
		where = " WHERE m.vehicle_id = ?"
		args = append(args, vehicleIDFilter)
	}

	var total int
	if err := DB.QueryRow("SELECT COUNT(*) FROM maintenance_logs m"+where, args...).Scan(&total); err != nil {
		log.Printf("❌ maintenance count: %v", err)
		http.Error(w, "Ошибка БД", http.StatusInternalServerError)
		return
	}

	sort, order := normalizeMaintenanceSort(r.FormValue("sort"))
	pagination := buildPagination(r, total, 50)
	offset := (pagination.Page - 1) * pagination.PageSize

	query := `
		SELECT m.id,
		       m.act_number,
		       strftime('%d.%m.%Y', m.service_date),
		       m.mileage,
		       v.license_plate,
		       v.brand_model,
		       m.oil_changed,
		       m.oil_filter_changed,
		       m.air_filter_changed,
		       m.front_pads_changed,
		       m.rear_pads_changed,
		       m.spark_plugs_changed,
		       m.timing_belt_changed,
		       m.timing_chain_changed,
		       COALESCE(m.other_work,''),
		       COALESCE(m.service_name,''),
		       COALESCE(m.cost,0),
		       COALESCE(m.attachment,'')
		FROM maintenance_logs m
		JOIN vehicles v ON v.id = m.vehicle_id` + where +
		" ORDER BY " + order + " LIMIT ? OFFSET ?"

	queryArgs := make([]interface{}, 0, len(args)+2)
	queryArgs = append(queryArgs, args...)
	queryArgs = append(queryArgs, pagination.PageSize, offset)

	rows, err := DB.Query(query, queryArgs...)
	if err != nil {
		log.Printf("❌ MaintenanceHandler Query: %v", err)
		http.Error(w, "Ошибка БД", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var logs []maintLog

	for rows.Next() {
		var l maintLog

		var oil, oilF, airF, frontP, rearP, plugs, belt, chain bool
		var otherWork string
		var cost float64

		if err := rows.Scan(
			&l.ID,
			&l.ActNumber,
			&l.ServiceDate,
			&l.Mileage,
			&l.LicensePlate,
			&l.BrandModel,
			&oil,
			&oilF,
			&airF,
			&frontP,
			&rearP,
			&plugs,
			&belt,
			&chain,
			&otherWork,
			&l.ServiceName,
			&cost,
			&l.Attachment,
		); err != nil {
			log.Printf("❌ MaintenanceHandler Scan: %v", err)
			continue
		}

		var works []string

		if oil || oilF || airF {
			works = append(works, "Масло/Фильтры")
		}
		if frontP {
			works = append(works, "Передние колодки")
		}
		if rearP {
			works = append(works, "Задние колодки")
		}
		if plugs {
			works = append(works, "Свечи")
		}
		if belt {
			works = append(works, "Ремень ГРМ")
		}
		if chain {
			works = append(works, "Цепь ГРМ")
		}
		if otherWork != "" {
			works = append(works, "Другое: "+otherWork)
		}

		l.WorkDone = strings.Join(works, ", ")
		l.Cost = strconv.FormatFloat(cost, 'f', 2, 64)

		logs = append(logs, l)
	}

	if err := rows.Err(); err != nil {
		log.Printf("❌ MaintenanceHandler rows.Err: %v", err)
	}

	var summary map[string]string
	if vehicleIDFilter > 0 {
		summary = getVehicleSummary(strconv.Itoa(vehicleIDFilter))
	}

	renderTemplate(w, "maintenance.html", map[string]interface{}{
		"Logs":          logs,
		"Vehicles":      activeVehicles(),
		"VehicleFilter": vehicleFilter,
		"Summary":       summary,
		"Sort":          sort,
		"Pagination":    pagination,
	})
}

func getVehicleSummary(vehicleIdStr string) map[string]string {
	vehicleId := parseOptInt(vehicleIdStr, 0)

	var currentMileage int
	var lastOil, lastFront, lastRear, lastPlugs, lastBelt, lastChain sql.NullInt64

	err := DB.QueryRow(`
		SELECT v.current_mileage,
			(SELECT MAX(mileage) FROM maintenance_logs ml
			 WHERE ml.vehicle_id=v.id AND (ml.oil_changed=1 OR ml.oil_filter_changed=1 OR ml.air_filter_changed=1)),
			(SELECT MAX(mileage) FROM maintenance_logs ml WHERE ml.vehicle_id=v.id AND ml.front_pads_changed=1),
			(SELECT MAX(mileage) FROM maintenance_logs ml WHERE ml.vehicle_id=v.id AND ml.rear_pads_changed=1),
			(SELECT MAX(mileage) FROM maintenance_logs ml WHERE ml.vehicle_id=v.id AND ml.spark_plugs_changed=1),
			(SELECT MAX(mileage) FROM maintenance_logs ml WHERE ml.vehicle_id=v.id AND ml.timing_belt_changed=1),
			(SELECT MAX(mileage) FROM maintenance_logs ml WHERE ml.vehicle_id=v.id AND ml.timing_chain_changed=1)
		FROM vehicles v
		WHERE v.id = ?`, vehicleId).Scan(
		&currentMileage,
		&lastOil,
		&lastFront,
		&lastRear,
		&lastPlugs,
		&lastBelt,
		&lastChain,
	)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("❌ getVehicleSummary: %v", err)
		}
		return nil
	}

	summary := make(map[string]string)

	fill := func(last sql.NullInt64, interval int, name string) {
		if !last.Valid {
			summary[name] = "Нет данных о последней замене"
			return
		}

		remaining := int(last.Int64) + interval - currentMileage
		if remaining > 0 {
			summary[name] = "Через " + strconv.Itoa(remaining) + " км"
		} else {
			summary[name] = "Просрочено на " + strconv.Itoa(-remaining) + " км"
		}
	}

	fill(lastOil, 7000, "Масло и фильтры")
	fill(lastFront, 25000, "Передние колодки")
	fill(lastRear, 70000, "Задние колодки")
	fill(lastPlugs, 40000, "Свечи зажигания")
	fill(lastBelt, 60000, "Ремень ГРМ")
	fill(lastChain, 150000, "Цепь ГРМ")

	return summary
}

func AddMaintenanceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		renderAddMaintenancePage(w, nil, MaintenanceForm{})
		return
	}

	form := maintenanceFormFromRequest(r)

	data, errs := validateMaintenanceForm(form)
	if len(errs) > 0 {
		renderAddMaintenancePage(w, errs, form)
		return
	}

	attachment, err := saveUploadedPDF(r, "attachment")
	if err != nil {
		renderAddMaintenancePage(w, []string{err.Error()}, form)
		return
	}

	if err := saveMaintenance(data, nil, attachment); err != nil {
		renderAddMaintenancePage(w, []string{
			"Не удалось сохранить запись о ТО. Проверьте данные и попробуйте ещё раз.",
		}, form)
		return
	}

	// Если пробег ТО больше текущего пробега машины — поднимаем его.
	raiseVehicleMileage(data.VehicleID, data.Mileage)

	http.Redirect(w, r, "/maintenance", http.StatusSeeOther)
}

func EditMaintenanceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		idStr := r.URL.Query().Get("id")

		id, ok := parseIntValue(idStr)
		if !ok || id <= 0 {
			http.Error(w, "Не указан ID записи о ТО", http.StatusBadRequest)
			return
		}

		var m struct {
			ID, VehicleID, Mileage, OtherRemindKm              int
			ActNumber, ServiceDate, ServiceName, OtherWork     string
			Cost                                               float64
			Attachment                                         string
			Oil, OilF, AirF, FrontP, RearP, Plugs, Belt, Chain bool
		}

		err := DB.QueryRow(`
			SELECT id, vehicle_id, act_number, service_date, mileage,
			       COALESCE(service_name,''), COALESCE(cost,0),
			       oil_changed, oil_filter_changed, air_filter_changed,
			       front_pads_changed, rear_pads_changed, spark_plugs_changed,
			       timing_belt_changed, timing_chain_changed,
			       COALESCE(other_work,''), other_remind_km, COALESCE(attachment,'')
			FROM maintenance_logs
			WHERE id = ?`, id).Scan(
			&m.ID,
			&m.VehicleID,
			&m.ActNumber,
			&m.ServiceDate,
			&m.Mileage,
			&m.ServiceName,
			&m.Cost,
			&m.Oil,
			&m.OilF,
			&m.AirF,
			&m.FrontP,
			&m.RearP,
			&m.Plugs,
			&m.Belt,
			&m.Chain,
			&m.OtherWork,
			&m.OtherRemindKm,
			&m.Attachment,
		)
		if err != nil {
			log.Printf("❌ query maintenance for edit: %v", err)
			http.Error(w, "Запись о ТО не найдена", http.StatusNotFound)
			return
		}

		form := MaintenanceForm{
			ID:            strconv.Itoa(m.ID),
			ActNumber:     m.ActNumber,
			VehicleID:     strconv.Itoa(m.VehicleID),
			ServiceDate:   m.ServiceDate,
			Mileage:       strconv.Itoa(m.Mileage),
			ServiceName:   m.ServiceName,
			Cost:          strconv.FormatFloat(m.Cost, 'f', -1, 64),
			Oil:           m.Oil,
			OilFilter:     m.OilF,
			AirFilter:     m.AirF,
			FrontPads:     m.FrontP,
			RearPads:      m.RearP,
			SparkPlugs:    m.Plugs,
			TimingBelt:    m.Belt,
			TimingChain:   m.Chain,
			OtherWork:     m.OtherWork,
			OtherRemindKm: strconv.Itoa(m.OtherRemindKm),
		}

		renderEditMaintenancePage(w, nil, form, m)
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "Ошибка формы", http.StatusBadRequest)
		return
	}

	form := maintenanceFormFromRequest(r)

	id, ok := parseIntValue(form.ID)
	if !ok || id <= 0 {
		http.Error(w, "Не указан ID записи о ТО", http.StatusBadRequest)
		return
	}

	data, errs := validateMaintenanceForm(form)
	if len(errs) > 0 {
		renderEditMaintenancePage(w, errs, form, struct{ ID int }{id})
		return
	}

	var currentAttach string
	_ = DB.QueryRow(`SELECT COALESCE(attachment,'') FROM maintenance_logs WHERE id=?`, id).Scan(&currentAttach)

	newAttach, err := saveUploadedPDF(r, "attachment")
	if err != nil {
		renderEditMaintenancePage(w, []string{err.Error()}, form, struct{ ID int }{id})
		return
	}

	removeAttach := r.FormValue("remove_attachment") == "on"

	attachment := currentAttach
	if newAttach != "" {
		deleteAttachmentFile(currentAttach)
		attachment = newAttach
	} else if removeAttach {
		deleteAttachmentFile(currentAttach)
		attachment = ""
	}

	if err := saveMaintenance(data, &id, attachment); err != nil {
		http.Error(w, "Не удалось сохранить изменения", http.StatusInternalServerError)
		return
	}

	// Если пробег ТО больше текущего пробега машины — поднимаем его.
	raiseVehicleMileage(data.VehicleID, data.Mileage)

	http.Redirect(w, r, "/maintenance", http.StatusSeeOther)
}

func raiseVehicleMileage(vehicleID, mileage int) {
	if vehicleID <= 0 || mileage <= 0 {
		return
	}

	if err := execSQL(`
		UPDATE vehicles
		SET current_mileage = ?
		WHERE id = ? AND current_mileage < ?`,
		mileage, vehicleID, mileage); err != nil {
		log.Printf("⚠️ raiseVehicleMileage: %v", err)
	}
}

func saveMaintenance(d *maintenanceValidated, id *int, attachment string) error {
	args := []interface{}{
		d.VehicleID,
		d.ActNumber,
		d.ServiceDate,
		d.Mileage,
		d.ServiceName,
		d.Cost,
		boolInt(d.Oil),
		boolInt(d.OilFilter),
		boolInt(d.AirFilter),
		boolInt(d.FrontPads),
		boolInt(d.RearPads),
		boolInt(d.SparkPlugs),
		boolInt(d.TimingBelt),
		boolInt(d.TimingChain),
		d.OtherWork,
		d.OtherRemindKm,
		attachment,
	}

	if id == nil {
		return execSQL(`
			INSERT INTO maintenance_logs (
				vehicle_id, act_number, service_date, mileage, service_name, cost,
				oil_changed, oil_filter_changed, air_filter_changed, front_pads_changed, rear_pads_changed,
				spark_plugs_changed, timing_belt_changed, timing_chain_changed, other_work, other_remind_km,
				attachment
			) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			args...,
		)
	}

	args = append(args, *id)

	return execSQL(`
		UPDATE maintenance_logs
		SET vehicle_id=?, act_number=?, service_date=?, mileage=?, service_name=?, cost=?,
			oil_changed=?, oil_filter_changed=?, air_filter_changed=?, front_pads_changed=?, rear_pads_changed=?,
			spark_plugs_changed=?, timing_belt_changed=?, timing_chain_changed=?, other_work=?, other_remind_km=?,
			attachment=?
		WHERE id=?`,
		args...,
	)
}

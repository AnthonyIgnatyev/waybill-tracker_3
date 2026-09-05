package handlers

import (
	"database/sql"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func renderAddVehiclePage(w http.ResponseWriter, errs []string, form VehicleForm) {
	renderTemplate(w, "add_vehicle.html", map[string]interface{}{
		"Errors": errs,
		"Form":   form,
	})
}

func renderEditVehiclePage(w http.ResponseWriter, errs []string, form VehicleForm, oldPlate string) {
	renderTemplate(w, "edit_vehicle.html", map[string]interface{}{
		"Errors":   errs,
		"Form":     form,
		"OldPlate": oldPlate,
	})
}

func VehiclesHandler(w http.ResponseWriter, r *http.Request) {
	type V struct {
		LicensePlate, BrandModel, AvgConsumption, TankCapacity string
		CurrentMileage                                         int
		DeletionReason, DeletionDate                           string
	}

	activeRows, err := DB.Query(`
        SELECT license_plate, brand_model, avg_consumption, current_mileage, tank_capacity
        FROM vehicles
        WHERE is_deleted = 0
        ORDER BY license_plate`)
	if err != nil {
		log.Printf("❌ query active vehicles: %v", err)
		http.Error(w, "Ошибка БД", http.StatusInternalServerError)
		return
	}
	defer activeRows.Close()

	var activeList []V

	for activeRows.Next() {
		var v V
		var avg, tank sql.NullFloat64

		if err := activeRows.Scan(&v.LicensePlate, &v.BrandModel, &avg, &v.CurrentMileage, &tank); err != nil {
			log.Printf("❌ scan active vehicle: %v", err)
			continue
		}

		v.AvgConsumption = formatNullFloat(avg)
		v.TankCapacity = formatNullFloat(tank)

		activeList = append(activeList, v)
	}

	if err := activeRows.Err(); err != nil {
		log.Printf("❌ activeRows.Err: %v", err)
	}

	deletedRows, err := DB.Query(`
        SELECT license_plate, brand_model, COALESCE(deletion_reason,''), COALESCE(deletion_date,'')
        FROM vehicles
        WHERE is_deleted = 1
        ORDER BY license_plate`)
	if err != nil {
		log.Printf("❌ query deleted vehicles: %v", err)
		http.Error(w, "Ошибка БД", http.StatusInternalServerError)
		return
	}
	defer deletedRows.Close()

	var deletedList []V

	for deletedRows.Next() {
		var v V

		if err := deletedRows.Scan(&v.LicensePlate, &v.BrandModel, &v.DeletionReason, &v.DeletionDate); err != nil {
			log.Printf("❌ scan deleted vehicle: %v", err)
			continue
		}

		deletedList = append(deletedList, v)
	}

	if err := deletedRows.Err(); err != nil {
		log.Printf("❌ deletedRows.Err: %v", err)
	}

	renderTemplate(w, "vehicles.html", map[string]interface{}{
		"ActiveVehicles":  activeList,
		"DeletedVehicles": deletedList,
		"Error":           r.URL.Query().Get("error"),
	})
}

func AddVehicleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		renderAddVehiclePage(w, nil, VehicleForm{})
		return
	}

	if err := r.ParseForm(); err != nil {
		renderAddVehiclePage(w, []string{"Ошибка данных формы."}, VehicleForm{})
		return
	}

	form := vehicleFormFromRequest(r)

	data, errs := validateVehicleForm(form, "")
	if len(errs) > 0 {
		renderAddVehiclePage(w, errs, form)
		return
	}

	err := execSQL(`
        INSERT INTO vehicles (
            license_plate,
            brand_model,
            avg_consumption,
            current_mileage,
            tank_capacity,
            is_deleted
        ) VALUES (?,?,?,?,?,0)`,
		data.Plate,
		data.BrandModel,
		data.AvgConsumption,
		data.CurrentMileage,
		data.TankCapacity,
	)
	if err != nil {
		renderAddVehiclePage(w, []string{
			"Не удалось сохранить автомобиль. Проверьте данные и попробуйте ещё раз.",
		}, form)
		return
	}

	http.Redirect(w, r, "/vehicles", http.StatusSeeOther)
}

func EditVehicleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		plate := r.URL.Query().Get("license_plate")

		var v struct {
			LicensePlate, BrandModel string
			AvgConsumption           float64
			CurrentMileage           int
			TankCapacity             float64
			OilInterval              int
		}

		err := DB.QueryRow(`
            SELECT license_plate, brand_model, COALESCE(avg_consumption,0), current_mileage,
                   COALESCE(tank_capacity,0), oil_interval
            FROM vehicles
            WHERE license_plate = ?`, plate).Scan(
			&v.LicensePlate,
			&v.BrandModel,
			&v.AvgConsumption,
			&v.CurrentMileage,
			&v.TankCapacity,
			&v.OilInterval,
		)
		if err != nil {
			log.Printf("❌ query vehicle for edit: %v", err)
			http.Error(w, "Автомобиль не найден", http.StatusNotFound)
			return
		}

		form := VehicleForm{
			LicensePlate:   v.LicensePlate,
			BrandModel:     v.BrandModel,
			AvgConsumption: strconv.FormatFloat(v.AvgConsumption, 'f', -1, 64),
			CurrentMileage: strconv.Itoa(v.CurrentMileage),
			TankCapacity:   strconv.FormatFloat(v.TankCapacity, 'f', -1, 64),
			OilInterval:    strconv.Itoa(v.OilInterval),
		}

		renderEditVehiclePage(w, nil, form, v.LicensePlate)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ошибка формы", http.StatusBadRequest)
		return
	}

	oldPlate := strings.TrimSpace(r.FormValue("old_license_plate"))
	if oldPlate == "" {
		http.Error(w, "Не указан исходный госномер", http.StatusBadRequest)
		return
	}

	form := vehicleFormFromRequest(r)

	data, errs := validateVehicleForm(form, oldPlate)
	if len(errs) > 0 {
		renderEditVehiclePage(w, errs, form, oldPlate)
		return
	}

	if oldPlate != data.Plate {
		if historyErr := execSQL(`
            INSERT INTO vehicle_number_history (
                vehicle_license_plate,
                old_number,
                new_number,
                change_date,
                comment
            ) VALUES (?,?,?,?,?)`,
			oldPlate,
			oldPlate,
			data.Plate,
			time.Now().Format("2006-01-02 15:04"),
			"Замена госномера",
		); historyErr != nil {
			log.Printf("⚠️ vehicle_number_history: %v", historyErr)
		}
	}

	err := execSQL(`
        UPDATE vehicles
        SET license_plate = ?, brand_model = ?, avg_consumption = ?,
            current_mileage = ?, tank_capacity = ?, oil_interval = ?
        WHERE license_plate = ?`,
		data.Plate,
		data.BrandModel,
		data.AvgConsumption,
		data.CurrentMileage,
		data.TankCapacity,
		data.OilInterval,
		oldPlate,
	)
	if err != nil {
		renderEditVehiclePage(w, []string{
			"Не удалось сохранить изменения. Проверьте данные и попробуйте ещё раз.",
		}, form, oldPlate)
		return
	}

	http.Redirect(w, r, "/vehicles", http.StatusSeeOther)
}

func DeleteVehicleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/vehicles", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ошибка формы", http.StatusBadRequest)
		return
	}

	reason := strings.TrimSpace(r.FormValue("deletion_reason"))
	plate := strings.TrimSpace(r.FormValue("license_plate"))

	if reason == "" {
		http.Redirect(w, r, "/vehicles?error=no_reason", http.StatusSeeOther)
		return
	}

	if plate == "" {
		http.Error(w, "Не указан автомобиль", http.StatusBadRequest)
		return
	}

	if err := execSQL(`
        UPDATE vehicles
        SET is_deleted = 1, deletion_reason = ?, deletion_date = date('now')
        WHERE license_plate = ?`,
		reason,
		plate,
	); err != nil {
		http.Error(w, "Не удалось списать автомобиль", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/vehicles", http.StatusSeeOther)
}

func RestoreVehicleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/vehicles", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ошибка формы", http.StatusBadRequest)
		return
	}

	plate := strings.TrimSpace(r.FormValue("license_plate"))
	if plate == "" {
		http.Error(w, "Не указан автомобиль", http.StatusBadRequest)
		return
	}

	if err := execSQL(`
        UPDATE vehicles
        SET is_deleted = 0, deletion_reason = NULL, deletion_date = NULL
        WHERE license_plate = ?`,
		plate,
	); err != nil {
		http.Error(w, "Не удалось восстановить автомобиль", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/vehicles", http.StatusSeeOther)
}

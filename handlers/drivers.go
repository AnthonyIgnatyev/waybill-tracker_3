package handlers

import (
	"log"
	"net/http"
)

type driverRow struct {
	ID            int
	LastName      string
	LicenseNumber string
}

type driverEdit struct {
	ID            int
	LastName      string
	LicenseNumber string
}

func fetchDrivers(fired int) []driverRow {
	rows, err := DB.Query(`
		SELECT id, last_name, COALESCE(license_number,'')
		FROM drivers
		WHERE is_fired = ?
		ORDER BY last_name`, fired)
	if err != nil {
		log.Printf("❌ fetchDrivers Query: %v", err)
		return nil
	}
	defer rows.Close()

	var list []driverRow

	for rows.Next() {
		var d driverRow
		if err := rows.Scan(&d.ID, &d.LastName, &d.LicenseNumber); err != nil {
			log.Printf("❌ fetchDrivers Scan: %v", err)
			continue
		}
		list = append(list, d)
	}

	if err := rows.Err(); err != nil {
		log.Printf("❌ fetchDrivers rows.Err: %v", err)
	}

	return list
}

func renderDriversPage(w http.ResponseWriter, errs []string, form map[string]interface{}) {
	if form == nil {
		form = map[string]interface{}{
			"LastName":      "",
			"LicenseNumber": "",
		}
	}

	renderTemplate(w, "drivers.html", map[string]interface{}{
		"ActiveDrivers": fetchDrivers(0),
		"FiredDrivers":  fetchDrivers(1),
		"Errors":        errs,
		"Form":          form,
	})
}

func DriversHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			renderDriversPage(w, []string{"Ошибка данных формы."}, nil)
			return
		}

		lastName := limitString(r.FormValue("last_name"), 100)
		license := limitString(r.FormValue("license_number"), 50)

		var errs []string
		if lastName == "" {
			errs = append(errs, "Укажите фамилию водителя.")
		}

		if len(errs) > 0 {
			renderDriversPage(w, errs, map[string]interface{}{
				"LastName":      r.FormValue("last_name"),
				"LicenseNumber": r.FormValue("license_number"),
			})
			return
		}

		if err := execSQL(
			"INSERT INTO drivers (last_name, license_number) VALUES (?,?)",
			lastName, license,
		); err != nil {
			renderDriversPage(w, []string{
				"Не удалось сохранить водителя. Попробуйте ещё раз.",
			}, map[string]interface{}{
				"LastName":      lastName,
				"LicenseNumber": license,
			})
			return
		}

		http.Redirect(w, r, "/drivers", http.StatusSeeOther)
		return
	}

	renderDriversPage(w, nil, nil)
}

func EditDriverHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		id, ok := parseIntValue(r.URL.Query().Get("id"))
		if !ok || id <= 0 {
			http.Error(w, "Не указан ID водителя", http.StatusBadRequest)
			return
		}

		var d driverEdit
		err := DB.QueryRow(`
			SELECT id, last_name, COALESCE(license_number,'')
			FROM drivers
			WHERE id = ?`, id).Scan(&d.ID, &d.LastName, &d.LicenseNumber)
		if err != nil {
			log.Printf("❌ query driver for edit: %v", err)
			http.Error(w, "Водитель не найден", http.StatusNotFound)
			return
		}

		renderTemplate(w, "edit_driver.html", map[string]interface{}{
			"Driver": d,
			"Errors": nil,
		})
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ошибка формы", http.StatusBadRequest)
		return
	}

	id, ok := parseIntValue(r.FormValue("id"))
	if !ok || id <= 0 {
		http.Error(w, "Не указан ID водителя", http.StatusBadRequest)
		return
	}

	lastName := limitString(r.FormValue("last_name"), 100)
	license := limitString(r.FormValue("license_number"), 50)

	if lastName == "" {
		renderTemplate(w, "edit_driver.html", map[string]interface{}{
			"Driver": driverEdit{
				ID:            id,
				LastName:      r.FormValue("last_name"),
				LicenseNumber: r.FormValue("license_number"),
			},
			"Errors": []string{"Укажите фамилию водителя."},
		})
		return
	}

	if err := execSQL(
		"UPDATE drivers SET last_name=?, license_number=? WHERE id=?",
		lastName, license, id,
	); err != nil {
		http.Error(w, "Не удалось сохранить изменения", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/drivers", http.StatusSeeOther)
}

func FireDriverHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/drivers", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ошибка формы", http.StatusBadRequest)
		return
	}

	id := parseOptInt(r.FormValue("id"), 0)
	if id <= 0 {
		http.Error(w, "Не указан ID водителя", http.StatusBadRequest)
		return
	}

	if err := execSQL("UPDATE drivers SET is_fired = 1 WHERE id = ?", id); err != nil {
		http.Error(w, "Не удалось уволить водителя", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/drivers", http.StatusSeeOther)
}

func RestoreDriverHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/drivers", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ошибка формы", http.StatusBadRequest)
		return
	}

	id := parseOptInt(r.FormValue("id"), 0)
	if id <= 0 {
		http.Error(w, "Не указан ID водителя", http.StatusBadRequest)
		return
	}

	if err := execSQL("UPDATE drivers SET is_fired = 0 WHERE id = ?", id); err != nil {
		http.Error(w, "Не удалось восстановить водителя", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/drivers", http.StatusSeeOther)
}

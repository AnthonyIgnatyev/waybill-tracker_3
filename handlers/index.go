package handlers

import (
	"database/sql"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"
)

const (
	redLimitKm   = 1500
	beltYellowKm = 10000

	// Пороги для прогресс-бара ТО
	toWarningKm = 1500 // жёлтая зона
	toDangerKm  = 500  // красная зона
)

var monthNames = []string{
	"Январь", "Февраль", "Март", "Апрель", "Май", "Июнь",
	"Июль", "Август", "Сентябрь", "Октябрь", "Ноябрь", "Декабрь",
}

type ToAlert struct {
	VehiclePlate string
	VehicleModel string
	ItemName     string
	RemainingKm  int
	OverdueKm    int
	AlertLevel   string
	Overdue      bool
}

// ToProgress — одна строка прогресс-бара приближающегося ТО.
type ToProgress struct {
	VehiclePlate   string
	VehicleModel   string
	ItemName       string
	LastMileage    int // пробег последнего ТО
	NextMileage    int // пробег следующего ТО
	CurrentMileage int
	RemainingKm    int
	Percent        int    // 0..100, заполнение полоски
	Level          string // ok / warning / danger
	Overdue        bool
	OverdueKm      int
	StatusLabel    string
}

type AttentionItem struct {
	Kind     string
	Level    string
	Title    string
	Subtitle string
	Link     string
}

type RecentWaybill struct {
	ID           int
	Number       string
	LicensePlate string
	DriverName   string
	OpenDate     string
	Mileage      int
	Consumption  float64
	NoFuel       bool
	Status       string
	StatusLabel  string
}

type DashboardStats struct {
	MonthWaybills  int
	MonthMileage   int
	MonthFuel      float64
	ActiveVehicles int
	TotalVehicles  int
	MonthMaintCost float64
	ErrorWaybills  int
}

type DashboardData struct {
	Stats          DashboardStats
	Alerts         []ToAlert
	ToProgress     []ToProgress
	ToUrgentCount  int
	Attention      []AttentionItem
	RecentWaybills []RecentWaybill
	PeriodLabel    string
}

func IndexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	monthEnd := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1).Format("2006-01-02")

	data := DashboardData{
		PeriodLabel: monthNames[int(now.Month())-1] + " " + strconv.Itoa(now.Year()),
	}

	_ = DB.QueryRow(`
		SELECT COALESCE(COUNT(*),0)
		FROM waybills
		WHERE open_date >= ? AND open_date <= ?`,
		monthStart, monthEnd,
	).Scan(&data.Stats.MonthWaybills)

	_ = DB.QueryRow(`
		SELECT COALESCE(SUM(end_mileage - start_mileage), 0),
		       COALESCE(SUM(fuel_added), 0)
		FROM waybills
		WHERE open_date >= ? AND open_date <= ?`,
		monthStart, monthEnd,
	).Scan(&data.Stats.MonthMileage, &data.Stats.MonthFuel)

	_ = DB.QueryRow(`
		SELECT COUNT(DISTINCT vehicle_id)
		FROM waybills
		WHERE open_date >= ? AND open_date <= ?`,
		monthStart, monthEnd,
	).Scan(&data.Stats.ActiveVehicles)

	_ = DB.QueryRow(`
		SELECT COALESCE(COUNT(*),0)
		FROM vehicles
		WHERE is_deleted = 0`,
	).Scan(&data.Stats.TotalVehicles)

	_ = DB.QueryRow(`
		SELECT COALESCE(SUM(cost), 0)
		FROM maintenance_logs
		WHERE service_date >= ? AND service_date <= ?`,
		monthStart, monthEnd,
	).Scan(&data.Stats.MonthMaintCost)

	_ = DB.QueryRow(`
		SELECT COUNT(*)
		FROM waybills
		WHERE (mileage_mismatch = 1 OR no_fuel_flag = 1)
		  AND open_date >= ? AND open_date <= ?`,
		monthStart, monthEnd,
	).Scan(&data.Stats.ErrorWaybills)

	data.Alerts = collectToAlerts()
	data.ToProgress = collectToProgress()

	urgent := 0
	for _, p := range data.ToProgress {
		if p.Level != "ok" {
			urgent++
		}
	}
	data.ToUrgentCount = urgent

	data.Attention = collectAttention(data.Alerts)
	data.RecentWaybills = collectRecentWaybills()

	renderTemplate(w, "index.html", data)
}

// collectToProgress строит рейтинг машин по приближающемуся ТО.
// Сверху — машина с минимальным остатком км. Не более 10 строк.
func collectToProgress() []ToProgress {
	best := make(map[string]ToProgress)

	// consider добавляет кандидат-пункт ТО и оставляет самый «горящий» для машины.
	consider := func(plate, model, name string, current, lastM, interval int) {
		if interval <= 0 {
			return
		}

		next := lastM + interval
		remaining := next - current

		percent := (current - lastM) * 100 / interval
		if percent < 0 {
			percent = 0
		}
		if percent > 100 {
			percent = 100 // не даём полоске выйти за границы
		}

		level := "ok"
		if remaining <= toDangerKm {
			level = "danger"
		} else if remaining <= toWarningKm {
			level = "warning"
		}

		p := ToProgress{
			VehiclePlate:   plate,
			VehicleModel:   model,
			ItemName:       name,
			LastMileage:    lastM,
			NextMileage:    next,
			CurrentMileage: current,
			RemainingKm:    remaining,
			Percent:        percent,
			Level:          level,
		}

		if remaining < 0 {
			p.Overdue = true
			p.OverdueKm = -remaining
			p.StatusLabel = "Просрочено на " + strconv.Itoa(p.OverdueKm) + " км"
		} else {
			p.StatusLabel = "Осталось " + strconv.Itoa(remaining) + " км"
		}

		if cur, ok := best[plate]; !ok || remaining < cur.RemainingKm {
			best[plate] = p
		}
	}

	// Стандартные пункты ТО
	rows, err := DB.Query(`
		SELECT v.license_plate, v.brand_model, v.current_mileage, v.oil_interval,
			(SELECT MAX(mileage) FROM maintenance_logs ml
			 WHERE ml.vehicle_id=v.id
			   AND (ml.oil_changed=1 OR ml.oil_filter_changed=1 OR ml.air_filter_changed=1)),
			(SELECT MAX(mileage) FROM maintenance_logs ml WHERE ml.vehicle_id=v.id AND ml.front_pads_changed=1),
			(SELECT MAX(mileage) FROM maintenance_logs ml WHERE ml.vehicle_id=v.id AND ml.rear_pads_changed=1),
			(SELECT MAX(mileage) FROM maintenance_logs ml WHERE ml.vehicle_id=v.id AND ml.spark_plugs_changed=1),
			(SELECT MAX(mileage) FROM maintenance_logs ml WHERE ml.vehicle_id=v.id AND ml.timing_belt_changed=1),
			(SELECT MAX(mileage) FROM maintenance_logs ml WHERE ml.vehicle_id=v.id AND ml.timing_chain_changed=1)
		FROM vehicles v WHERE v.is_deleted = 0`)
	if err != nil {
		log.Printf("❌ collectToProgress: %v", err)
	} else {
		defer rows.Close()

		for rows.Next() {
			var plate, model string
			var currentMileage, oilInterval int
			var lastOil, lastFront, lastRear, lastPlugs, lastBelt, lastChain sql.NullInt64

			if err := rows.Scan(
				&plate, &model, &currentMileage, &oilInterval,
				&lastOil, &lastFront, &lastRear, &lastPlugs, &lastBelt, &lastChain,
			); err != nil {
				continue
			}

			if lastOil.Valid {
				consider(plate, model, "Масло и фильтры", currentMileage, int(lastOil.Int64), oilInterval)
			}
			if lastFront.Valid {
				consider(plate, model, "Передние тормозные колодки", currentMileage, int(lastFront.Int64), 25000)
			}
			if lastRear.Valid {
				consider(plate, model, "Задние тормозные колодки", currentMileage, int(lastRear.Int64), 70000)
			}
			if lastPlugs.Valid {
				consider(plate, model, "Свечи зажигания", currentMileage, int(lastPlugs.Int64), 40000)
			}
			if lastBelt.Valid {
				consider(plate, model, "Ремень ГРМ", currentMileage, int(lastBelt.Int64), 60000)
			}
			if lastChain.Valid {
				consider(plate, model, "Цепь ГРМ", currentMileage, int(lastChain.Int64), 150000)
			}
		}
	}

	// Кастомные напоминания «Другое»
	customRows, err := DB.Query(`
		SELECT v.license_plate, v.brand_model, v.current_mileage,
		       m.other_work, m.mileage, m.other_remind_km
		FROM maintenance_logs m
		JOIN vehicles v ON v.id = m.vehicle_id
		WHERE m.other_work != ''
		  AND m.other_remind_km > 0
		  AND v.is_deleted = 0`)
	if err != nil {
		log.Printf("❌ collectToProgress custom: %v", err)
	} else {
		defer customRows.Close()

		for customRows.Next() {
			var plate, model, work string
			var cur, logKm, remind int

			if err := customRows.Scan(&plate, &model, &cur, &work, &logKm, &remind); err != nil {
				continue
			}

			consider(plate, model, work, cur, logKm, remind)
		}
	}

	// Собираем, сортируем по остатку (сверху самый горящий), берём не более 10
	list := make([]ToProgress, 0, len(best))
	for _, p := range best {
		list = append(list, p)
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].RemainingKm < list[j].RemainingKm
	})

	if len(list) > 10 {
		list = list[:10]
	}

	return list
}

func collectToAlerts() []ToAlert {
	var alerts []ToAlert

	rows, err := DB.Query(`
		SELECT v.license_plate, v.brand_model, v.current_mileage, v.oil_interval,
			(SELECT MAX(mileage) FROM maintenance_logs ml
			 WHERE ml.vehicle_id=v.id
			   AND (ml.oil_changed=1 OR ml.oil_filter_changed=1 OR ml.air_filter_changed=1)),
			(SELECT MAX(mileage) FROM maintenance_logs ml
			 WHERE ml.vehicle_id=v.id AND ml.front_pads_changed=1),
			(SELECT MAX(mileage) FROM maintenance_logs ml
			 WHERE ml.vehicle_id=v.id AND ml.rear_pads_changed=1),
			(SELECT MAX(mileage) FROM maintenance_logs ml
			 WHERE ml.vehicle_id=v.id AND ml.spark_plugs_changed=1),
			(SELECT MAX(mileage) FROM maintenance_logs ml
			 WHERE ml.vehicle_id=v.id AND ml.timing_belt_changed=1),
			(SELECT MAX(mileage) FROM maintenance_logs ml
			 WHERE ml.vehicle_id=v.id AND ml.timing_chain_changed=1)
		FROM vehicles v WHERE v.is_deleted = 0`)
	if err != nil {
		log.Printf("❌ collectToAlerts: %v", err)
		return alerts
	}
	defer rows.Close()

	for rows.Next() {
		var plate, model string
		var currentMileage, oilInterval int
		var lastOil, lastFront, lastRear, lastPlugs, lastBelt, lastChain sql.NullInt64

		if err := rows.Scan(
			&plate, &model, &currentMileage, &oilInterval,
			&lastOil, &lastFront, &lastRear, &lastPlugs, &lastBelt, &lastChain,
		); err != nil {
			log.Printf("❌ collectToAlerts scan: %v", err)
			continue
		}

		check := func(last sql.NullInt64, interval, yellowLimit int, name string) {
			if !last.Valid {
				return
			}
			remaining := int(last.Int64) + interval - currentMileage
			switch {
			case remaining < 0:
				alerts = append(alerts, ToAlert{plate, model, name, remaining, -remaining, "red", true})
			case remaining <= redLimitKm:
				alerts = append(alerts, ToAlert{plate, model, name, remaining, 0, "red", false})
			case yellowLimit > 0 && remaining <= yellowLimit:
				alerts = append(alerts, ToAlert{plate, model, name, remaining, 0, "yellow", false})
			}
		}

		check(lastOil, oilInterval, 0, "Масло и фильтры")
		check(lastFront, 25000, 0, "Передние тормозные колодки")
		check(lastRear, 70000, 0, "Задние тормозные колодки")
		check(lastPlugs, 40000, 0, "Свечи зажигания")
		check(lastBelt, 60000, beltYellowKm, "Ремень ГРМ")
		check(lastChain, 150000, beltYellowKm, "Цепь ГРМ")
	}

	if err := rows.Err(); err != nil {
		log.Printf("❌ collectToAlerts rows.Err: %v", err)
	}

	return alerts
}

func collectAttention(alerts []ToAlert) []AttentionItem {
	var items []AttentionItem

	rows, err := DB.Query(`
		SELECT w.id, w.waybill_number, v.license_plate, d.last_name,
		       w.start_mileage,
		       COALESCE((SELECT w2.end_mileage FROM waybills w2
		                 WHERE w2.vehicle_id = w.vehicle_id
		                   AND CAST(w2.waybill_number AS INTEGER) < CAST(w.waybill_number AS INTEGER)
		                 ORDER BY CAST(w2.waybill_number AS INTEGER) DESC LIMIT 1), 0)
		FROM waybills w
		JOIN vehicles v ON v.id = w.vehicle_id
		JOIN drivers d ON d.id = w.driver_id
		WHERE w.mileage_mismatch = 1
		ORDER BY w.id DESC
		LIMIT 5`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, start, prev int
			var num, plate, driver string
			_ = rows.Scan(&id, &num, &plate, &driver, &start, &prev)
			gap := start - prev
			subtitle := plate + " · " + driver
			if gap > 0 {
				subtitle += " · разрыв " + strconv.Itoa(gap) + " км"
			}
			items = append(items, AttentionItem{
				Kind:     "mileage_gap",
				Level:    "danger",
				Title:    "ПЛ № " + num + " — разрыв пробега",
				Subtitle: subtitle,
				Link:     "/waybills/edit?id=" + strconv.Itoa(id),
			})
		}
	}

	rows2, err := DB.Query(`
		SELECT w.id, w.waybill_number, v.license_plate, d.last_name,
		       w.end_mileage - w.start_mileage,
		       COALESCE(v.avg_consumption, 0),
		       COALESCE(w.fuel_start, 0) + COALESCE(w.fuel_added, 0) - COALESCE(w.fuel_end, 0)
		FROM waybills w
		JOIN vehicles v ON v.id = w.vehicle_id
		JOIN drivers d ON d.id = w.driver_id
		WHERE w.no_fuel_flag = 0
		  AND (w.end_mileage - w.start_mileage) > 0
		  AND COALESCE(v.avg_consumption, 0) > 0
		  AND (COALESCE(w.fuel_start,0) + COALESCE(w.fuel_added,0) - COALESCE(w.fuel_end,0))
		      / ((w.end_mileage - w.start_mileage) * 1.0) * 100
		      - COALESCE(v.avg_consumption, 0) > 2
		ORDER BY w.id DESC
		LIMIT 5`)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var id, mileage int
			var num, plate, driver string
			var norm, actual float64
			_ = rows2.Scan(&id, &num, &plate, &driver, &mileage, &norm, &actual)
			per100 := 0.0
			if mileage > 0 {
				per100 = actual / float64(mileage) * 100
			}
			items = append(items, AttentionItem{
				Kind:  "overconsumption",
				Level: "warning",
				Title: "ПЛ № " + num + " — перерасход топлива",
				Subtitle: plate + " · " + driver + " · " +
					strconv.FormatFloat(per100, 'f', 1, 64) +
					" л/100км (норма " + strconv.FormatFloat(norm, 'f', 1, 64) + ")",
				Link: "/waybills/edit?id=" + strconv.Itoa(id),
			})
		}
	}

	overdueCount := 0
	for _, a := range alerts {
		if a.Overdue {
			overdueCount++
		}
	}
	if overdueCount > 0 {
		items = append(items, AttentionItem{
			Kind:     "overdue_to",
			Level:    "danger",
			Title:    "Просроченных напоминаний о ТО: " + strconv.Itoa(overdueCount),
			Subtitle: "Требуется выполнить техническое обслуживание",
			Link:     "/maintenance",
		})
	}

	return items
}

func collectRecentWaybills() []RecentWaybill {
	rows, err := DB.Query(`
		SELECT w.id, w.waybill_number, v.license_plate, d.last_name,
		       strftime('%d.%m.%Y', w.open_date),
		       w.end_mileage - w.start_mileage,
		       COALESCE(v.avg_consumption, 0),
		       COALESCE(w.fuel_start, 0) + COALESCE(w.fuel_added, 0) - COALESCE(w.fuel_end, 0),
		       w.no_fuel_flag,
		       w.mileage_mismatch
		FROM waybills w
		JOIN vehicles v ON v.id = w.vehicle_id
		JOIN drivers d ON d.id = w.driver_id
		ORDER BY w.id DESC
		LIMIT 10`)
	if err != nil {
		log.Printf("❌ collectRecentWaybills: %v", err)
		return nil
	}
	defer rows.Close()

	var list []RecentWaybill
	for rows.Next() {
		var r RecentWaybill
		var norm, actual float64
		var noFuel, mismatch bool

		if err := rows.Scan(
			&r.ID, &r.Number, &r.LicensePlate, &r.DriverName, &r.OpenDate,
			&r.Mileage, &norm, &actual, &noFuel, &mismatch,
		); err != nil {
			continue
		}

		r.NoFuel = noFuel

		r.Consumption = 0
		if r.Mileage > 0 && actual > 0 {
			r.Consumption = actual / float64(r.Mileage) * 100
		}

		switch {
		case mismatch:
			r.Status = "error"
			r.StatusLabel = "Разрыв пробега"
		case noFuel:
			r.Status = "warning"
			r.StatusLabel = "Без заправки"
		case norm > 0 && r.Consumption > 0 && r.Consumption-norm > 2:
			r.Status = "warning"
			r.StatusLabel = "Перерасход"
		default:
			r.Status = "ok"
			r.StatusLabel = "Норма"
		}

		list = append(list, r)
	}

	if err := rows.Err(); err != nil {
		log.Printf("❌ collectRecentWaybills rows.Err: %v", err)
	}

	return list
}

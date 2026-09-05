package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func limitString(s string, max int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max])
	}
	return s
}

func parseIntValue(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return v, true
}

func parseIntNonNegative(s string) (int, bool) {
	v, ok := parseIntValue(s)
	if !ok {
		return 0, false
	}
	if v < 0 {
		return 0, false
	}
	return v, true
}

func parseFloatValue(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func parseFloatNonNegative(s string) (float64, bool) {
	v, ok := parseFloatValue(s)
	if !ok {
		return 0, false
	}
	if v < 0 {
		return 0, false
	}
	return v, true
}

func parseNullableFloatNonNegative(s string) (interface{}, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, true
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return nil, false
	}
	return v, true
}

func parseDateValue(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	_, err := time.Parse("2006-01-02", s)
	if err != nil {
		return "", false
	}
	return s, true
}

func nullFloatToString(v sql.NullFloat64) string {
	if !v.Valid {
		return ""
	}
	return strconv.FormatFloat(v.Float64, 'f', -1, 64)
}

func checkboxValue(r *http.Request, name string) bool {
	return r.FormValue(name) == "on"
}

func vehicleExists(id int) bool {
	if id <= 0 {
		return false
	}
	var count int
	err := DB.QueryRow(`
		SELECT COUNT(*)
		FROM vehicles
		WHERE id = ? AND is_deleted = 0`, id).Scan(&count)
	return err == nil && count > 0
}

func driverExists(id int) bool {
	if id <= 0 {
		return false
	}
	var count int
	err := DB.QueryRow(`
		SELECT COUNT(*)
		FROM drivers
		WHERE id = ? AND is_fired = 0`, id).Scan(&count)
	return err == nil && count > 0
}

func waybillNumberExists(number string, excludeID int) bool {
	var count int
	var err error

	if excludeID <= 0 {
		err = DB.QueryRow(`
			SELECT COUNT(*)
			FROM waybills
			WHERE waybill_number = ?`, number).Scan(&count)
	} else {
		err = DB.QueryRow(`
			SELECT COUNT(*)
			FROM waybills
			WHERE waybill_number = ? AND id != ?`, number, excludeID).Scan(&count)
	}

	return err == nil && count > 0
}

func vehiclePlateExists(plate, excludePlate string) bool {
	var count int
	var err error

	if excludePlate == "" {
		err = DB.QueryRow(`
			SELECT COUNT(*)
			FROM vehicles
			WHERE license_plate = ?`, plate).Scan(&count)
	} else {
		err = DB.QueryRow(`
			SELECT COUNT(*)
			FROM vehicles
			WHERE license_plate = ? AND license_plate != ?`, plate, excludePlate).Scan(&count)
	}

	return err == nil && count > 0
}

// ---------- Путевые листы ----------

type WaybillForm struct {
	ID             string
	WaybillNumber  string
	VehicleID      string
	DriverID       string
	OpenDate       string
	CloseDate      string
	StartMileage   string
	EndMileage     string
	FuelStart      string
	FuelEnd        string
	FuelAdded      string
	NoFuelReasonOn bool
	NoFuelReason   string
}

func waybillFormFromRequest(r *http.Request) WaybillForm {
	return WaybillForm{
		ID:             r.FormValue("id"),
		WaybillNumber:  r.FormValue("waybill_number"),
		VehicleID:      r.FormValue("vehicle_id"),
		DriverID:       r.FormValue("driver_id"),
		OpenDate:       r.FormValue("open_date"),
		CloseDate:      r.FormValue("close_date"),
		StartMileage:   r.FormValue("start_mileage"),
		EndMileage:     r.FormValue("end_mileage"),
		FuelStart:      r.FormValue("fuel_start"),
		FuelEnd:        r.FormValue("fuel_end"),
		FuelAdded:      r.FormValue("fuel_added"),
		NoFuelReasonOn: checkboxValue(r, "no_fuel_reason_on"),
		NoFuelReason:   r.FormValue("no_fuel_reason"),
	}
}

type waybillValidated struct {
	Number       string
	VehicleID    int
	DriverID     int
	OpenDate     string
	CloseDate    string
	StartMileage int
	EndMileage   int
	FuelStart    interface{}
	FuelEnd      interface{}
	FuelAdded    float64
	NoFuel       bool
	NoFuelReason string
}

func validateWaybillForm(f WaybillForm, excludeID int) (*waybillValidated, []string) {
	var errs []string
	var d waybillValidated

	d.Number = limitString(f.WaybillNumber, 50)
	if d.Number == "" {
		errs = append(errs, "Укажите номер путевого листа.")
	}

	vid, ok := parseIntValue(f.VehicleID)
	if !ok || vid <= 0 {
		errs = append(errs, "Выберите автомобиль.")
	} else {
		d.VehicleID = vid
		if !vehicleExists(vid) {
			errs = append(errs, "Выбранный автомобиль не найден или списан.")
		}
	}

	did, ok := parseIntValue(f.DriverID)
	if !ok || did <= 0 {
		errs = append(errs, "Выберите водителя.")
	} else {
		d.DriverID = did
		if !driverExists(did) {
			errs = append(errs, "Выбранный водитель не найден или уволен.")
		}
	}

	openDate, okOpen := parseDateValue(f.OpenDate)
	if !okOpen {
		errs = append(errs, "Укажите корректную дату открытия.")
	} else {
		d.OpenDate = openDate
	}

	closeDate, okClose := parseDateValue(f.CloseDate)
	if !okClose {
		errs = append(errs, "Укажите корректную дату закрытия.")
	} else {
		d.CloseDate = closeDate
	}

	if okOpen && okClose && closeDate < openDate {
		errs = append(errs, "Дата закрытия не может быть раньше даты открытия.")
	}

	start, okStart := parseIntNonNegative(f.StartMileage)
	if !okStart {
		errs = append(errs, "Начальный пробег должен быть неотрицательным числом.")
	} else {
		d.StartMileage = start
	}

	end, okEnd := parseIntNonNegative(f.EndMileage)
	if !okEnd {
		errs = append(errs, "Конечный пробег должен быть неотрицательным числом.")
	} else {
		d.EndMileage = end
	}

	if okStart && okEnd && end < start {
		errs = append(errs, "Конечный пробег не может быть меньше начального.")
	}

	fuelStart, okFuelStart := parseNullableFloatNonNegative(f.FuelStart)
	if !okFuelStart {
		errs = append(errs, "Топливо при выезде должно быть неотрицательным числом.")
	} else {
		d.FuelStart = fuelStart
	}

	fuelEnd, okFuelEnd := parseNullableFloatNonNegative(f.FuelEnd)
	if !okFuelEnd {
		errs = append(errs, "Топливо при возврате должно быть неотрицательным числом.")
	} else {
		d.FuelEnd = fuelEnd
	}

	fuelAddedStr := strings.TrimSpace(f.FuelAdded)
	if fuelAddedStr == "" {
		d.FuelAdded = 0
	} else {
		fuelAdded, okFuelAdded := parseFloatNonNegative(f.FuelAdded)
		if !okFuelAdded {
			errs = append(errs, "Заправленное топливо должно быть неотрицательным числом.")
		} else {
			d.FuelAdded = fuelAdded
		}
	}

	// Автоматическая пометка «Без заправки»:
	// если заправлено 0 или поле пустое
	d.NoFuel = d.FuelAdded <= 0

	if f.NoFuelReasonOn {
		d.NoFuelReason = limitString(f.NoFuelReason, 255)
	}

	if len(errs) > 0 {
		return nil, errs
	}

	if waybillNumberExists(d.Number, excludeID) {
		errs = append(errs, "Путевой лист с таким номером уже существует.")
		return nil, errs
	}

	return &d, nil
}

// ---------- Автомобили ----------

type VehicleForm struct {
	LicensePlate   string
	BrandModel     string
	AvgConsumption string
	CurrentMileage string
	TankCapacity   string
	OilInterval    string
}

func vehicleFormFromRequest(r *http.Request) VehicleForm {
	return VehicleForm{
		LicensePlate:   r.FormValue("license_plate"),
		BrandModel:     r.FormValue("brand_model"),
		AvgConsumption: r.FormValue("avg_consumption"),
		CurrentMileage: r.FormValue("current_mileage"),
		TankCapacity:   r.FormValue("tank_capacity"),
		OilInterval:    r.FormValue("oil_interval"),
	}
}

type vehicleValidated struct {
	Plate          string
	BrandModel     string
	AvgConsumption interface{}
	CurrentMileage int
	TankCapacity   interface{}
	OilInterval    int
}

func validateVehicleForm(f VehicleForm, oldPlate string) (*vehicleValidated, []string) {
	var errs []string
	var d vehicleValidated

	plate := strings.ToUpper(limitString(f.LicensePlate, 20))
	if plate == "" {
		errs = append(errs, "Укажите госномер автомобиля.")
	} else {
		d.Plate = plate
	}

	d.BrandModel = limitString(f.BrandModel, 100)

	avg, okAvg := parseNullableFloatNonNegative(f.AvgConsumption)
	if !okAvg {
		errs = append(errs, "Средний расход должен быть неотрицательным числом.")
	} else {
		d.AvgConsumption = avg
	}

	currentStr := strings.TrimSpace(f.CurrentMileage)
	if currentStr == "" {
		d.CurrentMileage = 0
	} else {
		current, okCurrent := parseIntNonNegative(f.CurrentMileage)
		if !okCurrent {
			errs = append(errs, "Текущий пробег должен быть неотрицательным числом.")
		} else {
			d.CurrentMileage = current
		}
	}

	tank, okTank := parseNullableFloatNonNegative(f.TankCapacity)
	if !okTank {
		errs = append(errs, "Объём бака должен быть неотрицательным числом.")
	} else {
		d.TankCapacity = tank
	}

	oilStr := strings.TrimSpace(f.OilInterval)
	if oilStr == "" {
		d.OilInterval = 7000
	} else {
		oil, okOil := parseIntValue(f.OilInterval)
		if !okOil || oil < 1000 {
			errs = append(errs, "Интервал замены масла должен быть числом не меньше 1000 км.")
		} else {
			d.OilInterval = oil
		}
	}

	if len(errs) > 0 {
		return nil, errs
	}

	if plate != oldPlate && vehiclePlateExists(plate, oldPlate) {
		errs = append(errs, "Автомобиль с таким госномером уже существует.")
		return nil, errs
	}

	return &d, nil
}

// ---------- Техническое обслуживание ----------

type MaintenanceForm struct {
	ID            string
	ActNumber     string
	VehicleID     string
	ServiceDate   string
	Mileage       string
	ServiceName   string
	Cost          string
	Oil           bool
	OilFilter     bool
	AirFilter     bool
	FrontPads     bool
	RearPads      bool
	SparkPlugs    bool
	TimingBelt    bool
	TimingChain   bool
	OtherWork     string
	OtherRemindKm string
}

func maintenanceFormFromRequest(r *http.Request) MaintenanceForm {
	return MaintenanceForm{
		ID:            r.FormValue("id"),
		ActNumber:     r.FormValue("act_number"),
		VehicleID:     r.FormValue("vehicle_id"),
		ServiceDate:   r.FormValue("service_date"),
		Mileage:       r.FormValue("mileage"),
		ServiceName:   r.FormValue("service_name"),
		Cost:          r.FormValue("cost"),
		Oil:           checkboxValue(r, "oil"),
		OilFilter:     checkboxValue(r, "oil_filter"),
		AirFilter:     checkboxValue(r, "air_filter"),
		FrontPads:     checkboxValue(r, "front_pads"),
		RearPads:      checkboxValue(r, "rear_pads"),
		SparkPlugs:    checkboxValue(r, "spark_plugs"),
		TimingBelt:    checkboxValue(r, "timing_belt"),
		TimingChain:   checkboxValue(r, "timing_chain"),
		OtherWork:     r.FormValue("other_work"),
		OtherRemindKm: r.FormValue("other_remind_km"),
	}
}

type maintenanceValidated struct {
	VehicleID     int
	ActNumber     string
	ServiceDate   string
	Mileage       int
	ServiceName   string
	Cost          float64
	Oil           bool
	OilFilter     bool
	AirFilter     bool
	FrontPads     bool
	RearPads      bool
	SparkPlugs    bool
	TimingBelt    bool
	TimingChain   bool
	OtherWork     string
	OtherRemindKm int
}

func validateMaintenanceForm(f MaintenanceForm) (*maintenanceValidated, []string) {
	var errs []string
	var d maintenanceValidated

	d.ActNumber = limitString(f.ActNumber, 100)
	if d.ActNumber == "" {
		errs = append(errs, "Укажите номер акта выполненных работ.")
	}

	vid, ok := parseIntValue(f.VehicleID)
	if !ok || vid <= 0 {
		errs = append(errs, "Выберите автомобиль.")
	} else {
		d.VehicleID = vid
		if !vehicleExists(vid) {
			errs = append(errs, "Выбранный автомобиль не найден или списан.")
		}
	}

	serviceDate, okDate := parseDateValue(f.ServiceDate)
	if !okDate {
		errs = append(errs, "Укажите корректную дату проведения ТО.")
	} else {
		d.ServiceDate = serviceDate
	}

	mileage, okMileage := parseIntNonNegative(f.Mileage)
	if !okMileage {
		errs = append(errs, "Пробег на момент ТО должен быть неотрицательным числом.")
	} else {
		d.Mileage = mileage
	}

	d.ServiceName = limitString(f.ServiceName, 150)

	costStr := strings.TrimSpace(f.Cost)
	if costStr == "" {
		d.Cost = 0
	} else {
		cost, okCost := parseFloatNonNegative(f.Cost)
		if !okCost {
			errs = append(errs, "Стоимость работ должна быть неотрицательным числом.")
		} else {
			d.Cost = cost
		}
	}

	d.Oil = f.Oil
	d.OilFilter = f.OilFilter
	d.AirFilter = f.AirFilter
	d.FrontPads = f.FrontPads
	d.RearPads = f.RearPads
	d.SparkPlugs = f.SparkPlugs
	d.TimingBelt = f.TimingBelt
	d.TimingChain = f.TimingChain

	d.OtherWork = limitString(f.OtherWork, 255)

	remindStr := strings.TrimSpace(f.OtherRemindKm)
	if remindStr == "" {
		d.OtherRemindKm = 0
	} else {
		remind, okRemind := parseIntNonNegative(f.OtherRemindKm)
		if !okRemind {
			errs = append(errs, "Напоминание по пробегу должно быть неотрицательным числом.")
		} else {
			d.OtherRemindKm = remind
		}
	}

	if len(errs) > 0 {
		return nil, errs
	}

	return &d, nil
}

package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"waybill-tracker/handlers"
)

func main() {
	dbPath := flag.String("db", "waybill.db", "путь к файлу базы SQLite")
	addr := flag.String("addr", ":8080", "адрес и порт сервера")
	staticDir := flag.String("static", "static", "папка со статическими файлами")
	flag.Parse()

	// ---------- База данных ----------
	if _, err := handlers.InitDB(*dbPath); err != nil {
		log.Fatal("❌ Ошибка БД: ", err)
	}
	log.Println("✅ Подключено к", *dbPath)

	// ---------- Шаблоны ----------
	if err := handlers.LoadTemplates("templates"); err != nil {
		log.Fatal("❌ Ошибка загрузки шаблонов: ", err)
	}

	// ---------- Версия статики для сброса кэша ----------
	handlers.InitAssetVersion(*staticDir)

	// ---------- Пересчёт разрывов пробега ----------
	if err := handlers.RecalculateAllVehicles(); err != nil {
		log.Printf("⚠️ RecalculateAllVehicles: %v", err)
	}

	// ---------- Маршруты ----------
	mux := http.NewServeMux()

	mux.HandleFunc("/", handlers.IndexHandler)
	mux.HandleFunc("/waybills", handlers.WaybillsHandler)
	mux.HandleFunc("/waybill/add", handlers.AddWaybillHandler)
	mux.HandleFunc("/waybills/edit", handlers.EditWaybillHandler)

	mux.HandleFunc("/drivers", handlers.DriversHandler)
	mux.HandleFunc("/drivers/edit", handlers.EditDriverHandler)
	mux.HandleFunc("/drivers/fire", handlers.FireDriverHandler)
	mux.HandleFunc("/drivers/restore", handlers.RestoreDriverHandler)

	mux.HandleFunc("/reports", handlers.ReportsHandler)

	mux.HandleFunc("/vehicles", handlers.VehiclesHandler)
	mux.HandleFunc("/vehicles/add", handlers.AddVehicleHandler)
	mux.HandleFunc("/vehicles/edit", handlers.EditVehicleHandler)
	mux.HandleFunc("/vehicles/delete", handlers.DeleteVehicleHandler)
	mux.HandleFunc("/vehicles/restore", handlers.RestoreVehicleHandler)

	mux.HandleFunc("/maintenance", handlers.MaintenanceHandler)
	mux.HandleFunc("/maintenance/add", handlers.AddMaintenanceHandler)
	mux.HandleFunc("/maintenance/edit", handlers.EditMaintenanceHandler)
	mux.HandleFunc("/maintenance/file", handlers.MaintenanceFileHandler)

	// Экспорт в CSV
	mux.HandleFunc("/export/waybills", handlers.ExportWaybillsCSV)
	mux.HandleFunc("/export/vehicles-report", handlers.ExportVehiclesReportCSV)
	mux.HandleFunc("/export/drivers-report", handlers.ExportDriversReportCSV)

	// Статика с долгим кэшем (URL содержит ?v=хеш, поэтому обновления подхватываются сами)
	mux.Handle("/static/", handlers.CacheStatic(
		http.StripPrefix("/static/", http.FileServer(http.Dir(*staticDir))),
	))

	// ---------- Middleware ----------
	handler := handlers.RequestLogger(
		handlers.Recovery(
			handlers.SecurityHeaders(mux),
		),
	)

	// ---------- Сервер с таймаутами ----------
	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// ---------- Graceful shutdown ----------
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

		sig := <-sigChan
		log.Printf("Получен сигнал %v, завершение работы...", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("Ошибка при завершении сервера: %v", err)
		}
	}()

	// ---------- Запуск ----------
	log.Printf("🚀 Сервер: http://localhost%s", *addr)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal("❌ Ошибка сервера: ", err)
	}

	log.Println("Сервер остановлен")
}

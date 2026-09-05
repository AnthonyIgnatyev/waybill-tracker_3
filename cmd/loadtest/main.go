package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"waybill-tracker/handlers"
)

func main() {
	mode := flag.String("mode", "http", "режим: seed (генерация данных) или http (нагрузка)")
	dbPath := flag.String("db", "loadtest.db", "файл БД для генерации")
	waybills := flag.Int("waybills", 5000, "сколько путевых листов сгенерировать")
	n := flag.Int("n", 2000, "количество HTTP-запросов")
	c := flag.Int("c", 10, "параллельных клиентов")
	base := flag.String("base", "http://localhost:8080", "базовый адрес сервера")
	flag.Parse()

	if *mode == "seed" {
		seed(*dbPath, *waybills)
		return
	}

	load(*base, *n, *c)
}

// seed — наполняет тестовую БД данными.
func seed(dbPath string, count int) {
	if _, err := handlers.InitDB(dbPath); err != nil {
		log.Fatal(err)
	}

	db := handlers.DB

	const vCount, dCount = 10, 5

	for i := 1; i <= vCount; i++ {
		_, _ = db.Exec(`
            INSERT OR IGNORE INTO vehicles (
                id,
                license_plate,
                brand_model,
                avg_consumption,
                current_mileage,
                tank_capacity,
                oil_interval
            ) VALUES (?,?,?,?,?,?,?)`,
			i,
			fmt.Sprintf("TEST%03d", i),
			"Тестовая машина",
			8.0,
			100000,
			50,
			7000,
		)
	}

	for i := 1; i <= dCount; i++ {
		_, _ = db.Exec(`
            INSERT OR IGNORE INTO drivers (id, last_name)
            VALUES (?,?)`,
			i,
			fmt.Sprintf("Тестовый %d", i),
		)
	}

	tx, err := db.Begin()
	if err != nil {
		log.Fatal(err)
	}

	rnd := rand.New(rand.NewSource(42))
	num := 300000

	for i := 0; i < count; i++ {
		num++

		start := 100000 + i*100 + rnd.Intn(50)
		end := start + 50 + rnd.Intn(200)

		date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i/20)

		_, _ = tx.Exec(`
            INSERT OR IGNORE INTO waybills (
                waybill_number,
                vehicle_id,
                driver_id,
                open_date,
                close_date,
                start_mileage,
                end_mileage,
                fuel_start,
                fuel_end,
                fuel_added
            ) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			fmt.Sprintf("%d", num),
			rnd.Intn(vCount)+1,
			rnd.Intn(dCount)+1,
			date.Format("2006-01-02"),
			date.AddDate(0, 0, 1).Format("2006-01-02"),
			start,
			end,
			40.0,
			30.0,
			20.0,
		)
	}

	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}

	log.Printf("✅ Сгенерировано %d путевых листов в %s", count, dbPath)
}

// load — нагружает запущенный сервер.
func load(base string, n, c int) {
	base = strings.TrimRight(base, "/")

	paths := []string{
		"/",
		"/waybills",
		"/vehicles",
		"/drivers",
		"/maintenance",
		"/reports",
		"/reports?tab=drivers&all_time=on",
	}

	jobs := make(chan string)

	go func() {
		for i := 0; i < n; i++ {
			jobs <- paths[i%len(paths)]
		}
		close(jobs)
	}()

	var mu sync.Mutex
	var wg sync.WaitGroup
	var durations []time.Duration
	errors := 0

	startAll := time.Now()

	for w := 0; w < c; w++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			client := &http.Client{Timeout: 10 * time.Second}

			for p := range jobs {
				t0 := time.Now()
				resp, err := client.Get(base + p)

				mu.Lock()
				durations = append(durations, time.Since(t0))

				if err != nil || resp.StatusCode != http.StatusOK {
					errors++
				}
				mu.Unlock()

				if err == nil {
					_ = resp.Body.Close()
				}
			}
		}()
	}

	wg.Wait()

	total := time.Since(startAll)

	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})

	var sum time.Duration
	for _, d := range durations {
		sum += d
	}

	pct := func(q float64) time.Duration {
		if len(durations) == 0 {
			return 0
		}
		return durations[int(float64(len(durations)-1)*q)]
	}

	avg := time.Duration(0)
	if len(durations) > 0 {
		avg = sum / time.Duration(len(durations))
	}

	fmt.Println("\n========== РЕЗУЛЬТАТЫ НАГРУЗОЧНОГО ТЕСТА ==========")
	fmt.Printf("Запросов: %d | Ошибок: %d\n", n, errors)
	fmt.Printf("Общее время: %.2f сек | RPS: %.1f\n", total.Seconds(), float64(n)/total.Seconds())
	fmt.Printf("Среднее: %v | p95: %v | p99: %v | Макс: %v\n",
		avg,
		pct(0.95),
		pct(0.99),
		pct(1.0),
	)
}

package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/sessions"
	"github.com/joho/godotenv"
	"github.com/labstack/echo"
	"github.com/labstack/echo-contrib/session"
	"github.com/labstack/echo/middleware"
	funk "github.com/thoas/go-funk"
)

type User struct {
	ID        int64  `json:"id,omitempty"`
	Nickname  string `json:"nickname,omitempty"`
	LoginName string `json:"login_name,omitempty"`
	PassHash  string `json:"pass_hash,omitempty"`
}

type Event struct {
	ID       int64  `json:"id,omitempty"`
	Title    string `json:"title,omitempty"`
	PublicFg bool   `json:"public,omitempty"`
	ClosedFg bool   `json:"closed,omitempty"`
	Price    int64  `json:"price,omitempty"`

	Total   int                `json:"total"`
	Remains int                `json:"remains"`
	Sheets  map[string]*Sheets `json:"sheets,omitempty"`
}

type Sheets struct {
	Total   int      `json:"total"`
	Remains int      `json:"remains"`
	Detail  []*Sheet `json:"detail,omitempty"`
	Price   int64    `json:"price"`
}

type Sheet struct {
	ID    int64  `json:"-"`
	Rank  string `json:"-"`
	Num   int64  `json:"num"`
	Price int64  `json:"-"`

	Mine           bool       `json:"mine,omitempty"`
	Reserved       bool       `json:"reserved,omitempty"`
	ReservedAt     *time.Time `json:"-"`
	ReservedAtUnix int64      `json:"reserved_at,omitempty"`
}

type Reservation struct {
	ID         int64      `json:"id"`
	EventID    int64      `json:"-"`
	SheetID    int64      `json:"-"`
	UserID     int64      `json:"-"`
	ReservedAt *time.Time `json:"-"`
	CanceledAt *time.Time `json:"-"`

	Event          *Event `json:"event,omitempty"`
	SheetRank      string `json:"sheet_rank,omitempty"`
	SheetNum       int64  `json:"sheet_num,omitempty"`
	Price          int64  `json:"price,omitempty"`
	ReservedAtUnix int64  `json:"reserved_at,omitempty"`
	CanceledAtUnix int64  `json:"canceled_at,omitempty"`
}

type Administrator struct {
	ID        int64  `json:"id,omitempty"`
	Nickname  string `json:"nickname,omitempty"`
	LoginName string `json:"login_name,omitempty"`
	PassHash  string `json:"pass_hash,omitempty"`
}

func arrayToString(a []int64, delim string) string {
	return strings.Trim(strings.Replace(fmt.Sprint(a), " ", delim, -1), "[]")
}

func sessUserID(c echo.Context) int64 {
	sess, _ := session.Get("session", c)
	var userID int64
	if x, ok := sess.Values["user_id"]; ok {
		userID, _ = x.(int64)
	}
	return userID
}

func sessSetUserID(c echo.Context, id int64) {
	sess, _ := session.Get("session", c)
	sess.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   3600,
		HttpOnly: true,
	}
	sess.Values["user_id"] = id
	sess.Save(c.Request(), c.Response())
}

func sessDeleteUserID(c echo.Context) {
	sess, _ := session.Get("session", c)
	sess.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   3600,
		HttpOnly: true,
	}
	delete(sess.Values, "user_id")
	sess.Save(c.Request(), c.Response())
}

func sessAdministratorID(c echo.Context) int64 {
	sess, _ := session.Get("session", c)
	var administratorID int64
	if x, ok := sess.Values["administrator_id"]; ok {
		administratorID, _ = x.(int64)
	}
	return administratorID
}

func sessSetAdministratorID(c echo.Context, id int64) {
	sess, _ := session.Get("session", c)
	sess.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   3600,
		HttpOnly: true,
	}
	sess.Values["administrator_id"] = id
	sess.Save(c.Request(), c.Response())
}

func sessDeleteAdministratorID(c echo.Context) {
	sess, _ := session.Get("session", c)
	sess.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   3600,
		HttpOnly: true,
	}
	delete(sess.Values, "administrator_id")
	sess.Save(c.Request(), c.Response())
}

func loginRequired(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if _, err := getLoginUser(c); err != nil {
			return resError(c, "login_required", 401)
		}
		return next(c)
	}
}

func adminLoginRequired(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if _, err := getLoginAdministrator(c); err != nil {
			return resError(c, "admin_login_required", 401)
		}
		return next(c)
	}
}

func getLoginUser(c echo.Context) (*User, error) {
	userID := sessUserID(c)
	if userID == 0 {
		return nil, errors.New("not logged in")
	}
	var user User
	err := db.QueryRow("SELECT id, nickname FROM users WHERE id = ?", userID).Scan(&user.ID, &user.Nickname)
	return &user, err
}

func getLoginAdministrator(c echo.Context) (*Administrator, error) {
	administratorID := sessAdministratorID(c)
	if administratorID == 0 {
		return nil, errors.New("not logged in")
	}
	var administrator Administrator
	err := db.QueryRow("SELECT id, nickname FROM administrators WHERE id = ?", administratorID).Scan(&administrator.ID, &administrator.Nickname)
	return &administrator, err
}

func getEvents(all bool) ([]*Event, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Commit()

	rows, err := tx.Query("SELECT * FROM events ORDER BY id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*Event
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.Title, &event.PublicFg, &event.ClosedFg, &event.Price); err != nil {
			return nil, err
		}
		if !all && !event.PublicFg {
			continue
		}
		events = append(events, &event)
	}

	ids := funk.Map(events, func(x *Event) int64 {
		return x.ID
	})
	addedEvents, err := getEventsIn(ids.([]int64), -1)
	for i := range addedEvents {
		for k := range addedEvents[i].Sheets {
			addedEvents[i].Sheets[k].Detail = nil
		}
	}

	return addedEvents, nil
}

func getEventsIn(eventIDs []int64, loginUserID int64) ([]*Event, error) {
	var events []*Event
	var reservations []*Reservation
	inClause := arrayToString(eventIDs, ",")

	// EVENTS
	{
		sql := fmt.Sprintf("SELECT * FROM events WHERE id IN (%s)", inClause)
		eventRows, err := db.Query(sql)
		if err != nil {
			log.Fatal(err)
			return nil, err
		}
		defer eventRows.Close()
		for eventRows.Next() {
			var event Event
			if err := eventRows.Scan(&event.ID, &event.Title, &event.PublicFg, &event.ClosedFg, &event.Price); err != nil {
				log.Fatal(err)
				return nil, err
			}

			event.Sheets = map[string]*Sheets{
				"S": &Sheets{},
				"A": &Sheets{},
				"B": &Sheets{},
				"C": &Sheets{},
			}
			events = append(events, &event)
		}
	}

	// RESERVATIONS
	{
		sql := fmt.Sprintf("SELECT event_id, sheet_id, user_id, reserved_at FROM reservations WHERE event_id IN (%s) AND canceled_at IS NULL", inClause)
		reservationsRows, err := db.Query(sql)
		if err != nil {
			log.Fatal(err)
			return nil, err
		}
		defer reservationsRows.Close()
		for reservationsRows.Next() {
			var reservation Reservation
			if err := reservationsRows.Scan(&reservation.EventID, &reservation.SheetID, &reservation.UserID, &reservation.ReservedAt); err != nil {
				log.Fatal(err)
				return nil, err
			}
			reservations = append(reservations, &reservation)
		}
		sort.Slice(reservations, func(i, j int) bool { return reservations[i].ReservedAt.Before(*reservations[j].ReservedAt) })
	}

	// ADD INFO
	{
		for i := range events {
			found := funk.Filter(reservations, func(x *Reservation) bool {
				return x.EventID == events[i].ID
			}).([]*Reservation)

			err := addEventInfo(events[i], found, loginUserID)
			if err != nil {
				log.Fatal(err)
				return nil, err
			}
		}
	}

	return events, nil
}

func getEvent(eventID, loginUserID int64) (*Event, error) {
	var event Event
	if err := db.QueryRow("SELECT * FROM events WHERE id = ?", eventID).Scan(&event.ID, &event.Title, &event.PublicFg, &event.ClosedFg, &event.Price); err != nil {
		return nil, err
	}
	event.Sheets = map[string]*Sheets{
		"S": &Sheets{},
		"A": &Sheets{},
		"B": &Sheets{},
		"C": &Sheets{},
	}

	// ----- まず reservations 全部取得。その後APサーバで処理 -----
	var reservations []*Reservation
	reservationsRows, err := db.Query("SELECT event_id, sheet_id, user_id, reserved_at FROM reservations WHERE event_id = ? AND canceled_at IS NULL", event.ID)
	if err != nil {
		log.Fatal(err)
		return nil, err
	}
	defer reservationsRows.Close()
	for reservationsRows.Next() {
		var reservation Reservation
		if err := reservationsRows.Scan(&reservation.EventID, &reservation.SheetID, &reservation.UserID, &reservation.ReservedAt); err != nil {
			log.Fatal(err)
			return nil, err
		}
		reservations = append(reservations, &reservation)
	}
	sort.Slice(reservations, func(i, j int) bool { return reservations[i].ReservedAt.Before(*reservations[j].ReservedAt) })
	// ---------------------------------------

	// ----- シートを走査 ----------------------
	err = addEventInfo(&event, reservations, loginUserID)
	if err != nil {
		log.Fatal(err)
		return nil, err
	}
	// ---------------------------------------

	return &event, nil
}

func addEventInfo(event *Event, reservations []*Reservation, loginUserID int64) error {
	rows, err := db.Query("SELECT * FROM sheets ORDER BY `rank`, num")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var sheet Sheet
		if err := rows.Scan(&sheet.ID, &sheet.Rank, &sheet.Num, &sheet.Price); err != nil {
			return err
		}

		sheetCopy := Sheet{
			ID:    sheet.ID,
			Rank:  sheet.Rank,
			Num:   sheet.Num,
			Price: sheet.Price,
		}

		event.Sheets[sheetCopy.Rank].Price = event.Price + sheetCopy.Price
		event.Total++
		event.Sheets[sheetCopy.Rank].Total++

		// sheet_idでfind
		var reservation Reservation
		for _, v := range reservations {
			if v.SheetID == sheetCopy.ID {
				reservation = *v
				break
			}
		}

		if (Reservation{}) == reservation {
			// そのシートに予約が入っていない場合
			event.Remains++
			event.Sheets[sheetCopy.Rank].Remains++
		} else {
			// シートに予約が入っている場合、最もReservedAtが早いものを取得
			sheetCopy.Mine = reservation.UserID == loginUserID
			sheetCopy.Reserved = true
			sheetCopy.ReservedAtUnix = reservation.ReservedAt.Unix()
		}

		event.Sheets[sheetCopy.Rank].Detail = append(event.Sheets[sheetCopy.Rank].Detail, &sheetCopy)
	}

	return nil
}

func sanitizeEvent(e *Event) *Event {
	sanitized := *e
	sanitized.Price = 0
	sanitized.PublicFg = false
	sanitized.ClosedFg = false
	return &sanitized
}

func fillinUser(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if user, err := getLoginUser(c); err == nil {
			c.Set("user", user)
		}
		return next(c)
	}
}

func fillinAdministrator(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if administrator, err := getLoginAdministrator(c); err == nil {
			c.Set("administrator", administrator)
		}
		return next(c)
	}
}

func validateRank(rank string) bool {
	var count int
	db.QueryRow("SELECT COUNT(*) FROM sheets WHERE `rank` = ?", rank).Scan(&count)
	return count > 0
}

type Renderer struct {
	templates *template.Template
}

func (r *Renderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return r.templates.ExecuteTemplate(w, name, data)
}

var db *sql.DB

func main() {
	{
		err := godotenv.Load("../env.sh")
		if err != nil {
			log.Fatal("Error loading .env file")
		}
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4",
		os.Getenv("DB_USER"), os.Getenv("DB_PASS"),
		os.Getenv("DB_HOST"), os.Getenv("DB_PORT"),
		os.Getenv("DB_DATABASE"),
	)

	var err error
	db, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}

	e := echo.New()
	funcs := template.FuncMap{
		"encode_json": func(v interface{}) string {
			b, _ := json.Marshal(v)
			return string(b)
		},
	}
	e.Renderer = &Renderer{
		templates: template.Must(template.New("").Delims("[[", "]]").Funcs(funcs).ParseGlob("views/*.tmpl")),
	}
	e.Use(session.Middleware(sessions.NewCookieStore([]byte("secret"))))
	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		Format: "method=${method}, uri=${uri}, status=${status}, latency_human=${latency_human}\n",
		Output: os.Stderr,
	}))
	e.Static("/", "public")
	e.GET("/", func(c echo.Context) error {
		events, err := getEvents(false)
		if err != nil {
			return err
		}
		for i, v := range events {
			events[i] = sanitizeEvent(v)
		}
		return c.Render(200, "index.tmpl", echo.Map{
			"events": events,
			"user":   c.Get("user"),
			"origin": c.Scheme() + "://" + c.Request().Host,
		})
	}, fillinUser)
	e.GET("/initialize", func(c echo.Context) error {
		cmd := exec.Command("./src/torb/db/init.sh")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		err := cmd.Run()
		if err != nil {
			log.Fatal(err)
			panic(err)
		}

		// sheets
		var sheets []*Sheet
		var sheetMap map[int64]*Sheet
		{
			rows, err := db.Query("SELECT * FROM sheets")
			if err != nil {
				return err
			}
			defer rows.Close()

			for rows.Next() {
				var sheet Sheet
				if err := rows.Scan(&sheet.ID, &sheet.Rank, &sheet.Num, &sheet.Price); err != nil {
					return err
				}
				sheets = append(sheets, &sheet)
			}

			sheetMap = funk.Map(sheets, func(x *Sheet) (int64, *Sheet) {
				return x.ID, x
			}).(map[int64]*Sheet)
		}

		// insert non-reserved sheets
		{
			lenOfSheets := int64(1000)

			// 初期状態で空きがあるのはeventID11,12,13のみ
			for ei := 11; ei <= 13; ei++ {
				lenOfRows := lenOfSheets
				valueStrings := make([]string, 0, lenOfRows)
				valueArgs := make([]interface{}, 0, lenOfRows*2)

				for si := int64(1); si <= lenOfSheets; si++ {
					valueStrings = append(valueStrings, "(?,?,?)")
					valueArgs = append(valueArgs, ei)
					valueArgs = append(valueArgs, si)
					valueArgs = append(valueArgs, sheetMap[si].Rank)
				}

				stmt := fmt.Sprintf("INSERT INTO non_reserved_sheets (event_id, sheet_id, `rank`) VALUES %s", strings.Join(valueStrings, ","))
				_, err := db.Exec(stmt, valueArgs...)
				if err != nil {
					panic(err)
				}
			}

		}

		return c.NoContent(204)
	})
	e.POST("/api/users", func(c echo.Context) error {
		var params struct {
			Nickname  string `json:"nickname"`
			LoginName string `json:"login_name"`
			Password  string `json:"password"`
		}
		c.Bind(&params)

		tx, err := db.Begin()
		if err != nil {
			return err
		}

		var user User
		if err := tx.QueryRow("SELECT * FROM users WHERE login_name = ?", params.LoginName).Scan(&user.ID, &user.LoginName, &user.Nickname, &user.PassHash); err != sql.ErrNoRows {
			tx.Rollback()
			if err == nil {
				return resError(c, "duplicated", 409)
			}
			return err
		}

		res, err := tx.Exec("INSERT INTO users (login_name, pass_hash, nickname) VALUES (?, SHA2(?, 256), ?)", params.LoginName, params.Password, params.Nickname)
		if err != nil {
			tx.Rollback()
			return resError(c, "", 0)
		}
		userID, err := res.LastInsertId()
		if err != nil {
			tx.Rollback()
			return resError(c, "", 0)
		}
		if err := tx.Commit(); err != nil {
			return err
		}

		return c.JSON(201, echo.Map{
			"id":       userID,
			"nickname": params.Nickname,
		})
	})

	// TODO: tuning
	e.GET("/api/users/:id", func(c echo.Context) error {
		var user User
		if err := db.QueryRow("SELECT id, nickname FROM users WHERE id = ?", c.Param("id")).Scan(&user.ID, &user.Nickname); err != nil {
			return err
		}

		loginUser, err := getLoginUser(c)
		if err != nil {
			return err
		}
		if user.ID != loginUser.ID {
			return resError(c, "forbidden", 403)
		}

		rows, err := db.Query("SELECT r.*, s.rank AS sheet_rank, s.num AS sheet_num FROM reservations r INNER JOIN sheets s ON s.id = r.sheet_id WHERE r.user_id = ? ORDER BY IFNULL(r.canceled_at, r.reserved_at) DESC LIMIT 5", user.ID)
		if err != nil {
			return err
		}
		defer rows.Close()

		var recentReservations []Reservation
		for rows.Next() {
			var reservation Reservation
			var sheet Sheet
			if err := rows.Scan(&reservation.ID, &reservation.EventID, &reservation.SheetID, &reservation.UserID, &reservation.ReservedAt, &reservation.CanceledAt, &sheet.Rank, &sheet.Num); err != nil {
				return err
			}
			reservation.SheetRank = sheet.Rank
			reservation.SheetNum = sheet.Num
			reservation.ReservedAtUnix = reservation.ReservedAt.Unix()
			if reservation.CanceledAt != nil {
				reservation.CanceledAtUnix = reservation.CanceledAt.Unix()
			}
			recentReservations = append(recentReservations, reservation)
		}

		if recentReservations == nil {
			recentReservations = make([]Reservation, 0)
		} else {
			// eventまとめてfetch
			eventIDs := funk.Map(recentReservations, func(x Reservation) int64 {
				return x.EventID
			})
			events, err := getEventsIn(eventIDs.([]int64), -1)
			if err != nil {
				log.Fatal(err)
				return err
			}

			for i := range recentReservations {
				// 複数event取得後にアプリケーション側でFind
				foundEvent := funk.Find(events, func(x *Event) bool {
					return x.ID == recentReservations[i].EventID
				}).(*Event)

				// 同じEventIDの場合あるので構造体のコピーが必要
				cloned := *foundEvent

				price := cloned.Sheets[recentReservations[i].SheetRank].Price
				cloned.Sheets = nil
				cloned.Total = 0
				cloned.Remains = 0

				recentReservations[i].Price = price
				recentReservations[i].Event = &cloned
			}
		}

		var totalPrice int
		if err := db.QueryRow("SELECT IFNULL(SUM(e.price + s.price), 0) FROM reservations r INNER JOIN sheets s ON s.id = r.sheet_id INNER JOIN events e ON e.id = r.event_id WHERE r.user_id = ? AND r.canceled_at IS NULL", user.ID).Scan(&totalPrice); err != nil {
			return err
		}

		rows, err = db.Query("SELECT event_id FROM reservations WHERE user_id = ? GROUP BY event_id ORDER BY MAX(IFNULL(canceled_at, reserved_at)) DESC LIMIT 5", user.ID)
		if err != nil {
			return err
		}
		defer rows.Close()

		var eventIDs []int64
		for rows.Next() {
			var eventID int64
			if err := rows.Scan(&eventID); err != nil {
				return err
			}
			eventIDs = append(eventIDs, eventID)
		}

		// fetch events information
		var recentEvents []*Event
		if len(eventIDs) > 0 {
			recentEvents, err = getEventsIn(eventIDs, -1)
			if err != nil {
				log.Fatal(err)
				return err
			}
		}

		// sort with eventIDs order
		if len(recentEvents) > 0 {
			recentEvents = funk.Map(eventIDs, func(eid int64) *Event {
				return funk.Find(recentEvents, func(e *Event) bool {
					return e.ID == eid
				}).(*Event)
			}).([]*Event)
		}

		return c.JSON(200, echo.Map{
			"id":                  user.ID,
			"nickname":            user.Nickname,
			"recent_reservations": recentReservations,
			"total_price":         totalPrice,
			"recent_events":       recentEvents,
		})
	}, loginRequired)
	e.POST("/api/actions/login", func(c echo.Context) error {
		var params struct {
			LoginName string `json:"login_name"`
			Password  string `json:"password"`
		}
		c.Bind(&params)

		user := new(User)
		if err := db.QueryRow("SELECT * FROM users WHERE login_name = ?", params.LoginName).Scan(&user.ID, &user.LoginName, &user.Nickname, &user.PassHash); err != nil {
			if err == sql.ErrNoRows {
				return resError(c, "authentication_failed", 401)
			}
			return err
		}

		var passHash string
		if err := db.QueryRow("SELECT SHA2(?, 256)", params.Password).Scan(&passHash); err != nil {
			return err
		}
		if user.PassHash != passHash {
			return resError(c, "authentication_failed", 401)
		}

		sessSetUserID(c, user.ID)
		user, err = getLoginUser(c)
		if err != nil {
			return err
		}
		return c.JSON(200, user)
	})
	e.POST("/api/actions/logout", func(c echo.Context) error {
		sessDeleteUserID(c)
		return c.NoContent(204)
	}, loginRequired)
	e.GET("/api/events", func(c echo.Context) error {
		events, err := getEvents(true)
		if err != nil {
			return err
		}
		for i, v := range events {
			events[i] = sanitizeEvent(v)
		}
		return c.JSON(200, events)
	})
	e.GET("/api/events/:id", func(c echo.Context) error {
		eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			return resError(c, "not_found", 404)
		}

		loginUserID := int64(-1)
		if user, err := getLoginUser(c); err == nil {
			loginUserID = user.ID
		}

		event, err := getEvent(eventID, loginUserID)
		if err != nil {
			if err == sql.ErrNoRows {
				return resError(c, "not_found", 404)
			}
			return err
		} else if !event.PublicFg {
			return resError(c, "not_found", 404)
		}
		return c.JSON(200, sanitizeEvent(event))
	})
	e.POST("/api/events/:id/actions/reserve", func(c echo.Context) error {
		eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			return resError(c, "not_found", 404)
		}
		var params struct {
			Rank string `json:"sheet_rank"`
		}
		c.Bind(&params)

		user, err := getLoginUser(c)
		if err != nil {
			return err
		}

		event, err := getEvent(eventID, user.ID)
		if err != nil {
			if err == sql.ErrNoRows {
				return resError(c, "invalid_event", 404)
			}
			return err
		} else if !event.PublicFg {
			return resError(c, "invalid_event", 404)
		}

		if !validateRank(params.Rank) {
			return resError(c, "invalid_rank", 400)
		}

		// TODO: tuning
		var sheetID int64
		{
			tx, err := db.Begin()
			if err != nil {
				return err
			}

			var id int64
			if err := tx.QueryRow("SELECT id, sheet_id FROM non_reserved_sheets WHERE event_id = ? AND `rank` = ? ORDER BY RAND() LIMIT 1 FOR UPDATE", event.ID, params.Rank).Scan(&id, &sheetID); err != nil {
				if err == sql.ErrNoRows {
					tx.Commit()
					return resError(c, "sold_out", 409)
				}
				return err
			}

			_, err = tx.Exec("DELETE FROM non_reserved_sheets WHERE id = ?", id)
			if err := tx.Commit(); err != nil {
				tx.Rollback()
				return err
			}
		}

		var sheet Sheet
		var reservationID int64
		{
			if err := db.QueryRow("SELECT * FROM sheets WHERE id = ?", sheetID).Scan(&sheet.ID, &sheet.Rank, &sheet.Num, &sheet.Price); err != nil {
				return err
			}

			res, err := db.Exec("INSERT INTO reservations (event_id, sheet_id, user_id, reserved_at) VALUES (?, ?, ?, ?)", event.ID, sheet.ID, user.ID, time.Now().UTC().Format("2006-01-02 15:04:05.000000"))
			if err != nil {
				return err
			}
			reservationID, err = res.LastInsertId()
		}

		return c.JSON(202, echo.Map{
			"id":         reservationID,
			"sheet_rank": params.Rank,
			"sheet_num":  sheet.Num,
		})
	}, loginRequired)

	// TODO: tuning
	e.DELETE("/api/events/:id/sheets/:rank/:num/reservation", func(c echo.Context) error {
		eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			return resError(c, "not_found", 404)
		}
		rank := c.Param("rank")
		num := c.Param("num")

		user, err := getLoginUser(c)
		if err != nil {
			return err
		}

		event, err := getEvent(eventID, user.ID)
		if err != nil {
			if err == sql.ErrNoRows {
				return resError(c, "invalid_event", 404)
			}
			return err
		} else if !event.PublicFg {
			return resError(c, "invalid_event", 404)
		}

		if !validateRank(rank) {
			return resError(c, "invalid_rank", 404)
		}

		var sheet Sheet
		if err := db.QueryRow("SELECT * FROM sheets WHERE `rank` = ? AND num = ?", rank, num).Scan(&sheet.ID, &sheet.Rank, &sheet.Num, &sheet.Price); err != nil {
			if err == sql.ErrNoRows {
				return resError(c, "invalid_sheet", 404)
			}
			return err
		}

		tx, err := db.Begin()
		if err != nil {
			return err
		}

		var reservation Reservation
		if err := tx.QueryRow("SELECT * FROM reservations WHERE event_id = ? AND sheet_id = ? AND canceled_at IS NULL GROUP BY event_id HAVING reserved_at = MIN(reserved_at)", event.ID, sheet.ID).Scan(&reservation.ID, &reservation.EventID, &reservation.SheetID, &reservation.UserID, &reservation.ReservedAt, &reservation.CanceledAt); err != nil {
			tx.Rollback()
			if err == sql.ErrNoRows {
				return resError(c, "not_reserved", 400)
			}
			return err
		}
		if reservation.UserID != user.ID {
			tx.Rollback()
			return resError(c, "not_permitted", 403)
		}

		if _, err := tx.Exec("UPDATE reservations SET canceled_at = ? WHERE id = ?", time.Now().UTC().Format("2006-01-02 15:04:05.000000"), reservation.ID); err != nil {
			tx.Rollback()
			return err
		}

		if _, err = tx.Exec("INSERT INTO non_reserved_sheets (event_id, sheet_id, `rank`) VALUES (?, ?, ?)", event.ID, sheet.ID, sheet.Rank); err != nil {
			tx.Rollback()
			return err
		}

		if err := tx.Commit(); err != nil {
			return err
		}

		return c.NoContent(204)
	}, loginRequired)
	e.GET("/admin/", func(c echo.Context) error {
		var events []*Event
		administrator := c.Get("administrator")
		if administrator != nil {
			var err error
			if events, err = getEvents(true); err != nil {
				return err
			}
		}
		return c.Render(200, "admin.tmpl", echo.Map{
			"events":        events,
			"administrator": administrator,
			"origin":        c.Scheme() + "://" + c.Request().Host,
		})
	}, fillinAdministrator)
	e.POST("/admin/api/actions/login", func(c echo.Context) error {
		var params struct {
			LoginName string `json:"login_name"`
			Password  string `json:"password"`
		}
		c.Bind(&params)

		administrator := new(Administrator)
		if err := db.QueryRow("SELECT * FROM administrators WHERE login_name = ?", params.LoginName).Scan(&administrator.ID, &administrator.LoginName, &administrator.Nickname, &administrator.PassHash); err != nil {
			if err == sql.ErrNoRows {
				return resError(c, "authentication_failed", 401)
			}
			return err
		}

		var passHash string
		if err := db.QueryRow("SELECT SHA2(?, 256)", params.Password).Scan(&passHash); err != nil {
			return err
		}
		if administrator.PassHash != passHash {
			return resError(c, "authentication_failed", 401)
		}

		sessSetAdministratorID(c, administrator.ID)
		administrator, err = getLoginAdministrator(c)
		if err != nil {
			return err
		}
		return c.JSON(200, administrator)
	})
	e.POST("/admin/api/actions/logout", func(c echo.Context) error {
		sessDeleteAdministratorID(c)
		return c.NoContent(204)
	}, adminLoginRequired)
	e.GET("/admin/api/events", func(c echo.Context) error {
		events, err := getEvents(true)
		if err != nil {
			return err
		}
		return c.JSON(200, events)
	}, adminLoginRequired)
	e.POST("/admin/api/events", func(c echo.Context) error {
		var params struct {
			Title  string `json:"title"`
			Public bool   `json:"public"`
			Price  int    `json:"price"`
		}
		c.Bind(&params)

		tx, err := db.Begin()
		if err != nil {
			return err
		}

		res, err := tx.Exec("INSERT INTO events (title, public_fg, closed_fg, price) VALUES (?, ?, 0, ?)", params.Title, params.Public, params.Price)
		if err != nil {
			tx.Rollback()
			return err
		}
		eventID, err := res.LastInsertId()
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}

		event, err := getEvent(eventID, -1)
		if err != nil {
			return err
		}
		return c.JSON(200, event)
	}, adminLoginRequired)
	e.GET("/admin/api/events/:id", func(c echo.Context) error {
		eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			return resError(c, "not_found", 404)
		}
		event, err := getEvent(eventID, -1)
		if err != nil {
			if err == sql.ErrNoRows {
				return resError(c, "not_found", 404)
			}
			return err
		}
		return c.JSON(200, event)
	}, adminLoginRequired)
	e.POST("/admin/api/events/:id/actions/edit", func(c echo.Context) error {
		eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			return resError(c, "not_found", 404)
		}

		var params struct {
			Public bool `json:"public"`
			Closed bool `json:"closed"`
		}
		c.Bind(&params)
		if params.Closed {
			params.Public = false
		}

		event, err := getEvent(eventID, -1)
		if err != nil {
			if err == sql.ErrNoRows {
				return resError(c, "not_found", 404)
			}
			return err
		}

		if event.ClosedFg {
			return resError(c, "cannot_edit_closed_event", 400)
		} else if event.PublicFg && params.Closed {
			return resError(c, "cannot_close_public_event", 400)
		}

		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE events SET public_fg = ?, closed_fg = ? WHERE id = ?", params.Public, params.Closed, event.ID); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}

		e, err := getEvent(eventID, -1)
		if err != nil {
			return err
		}
		c.JSON(200, e)
		return nil
	}, adminLoginRequired)
	e.GET("/admin/api/reports/events/:id/sales", func(c echo.Context) error {
		eventID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			return resError(c, "not_found", 404)
		}

		event, err := getEvent(eventID, -1)
		if err != nil {
			return err
		}

		rows, err := db.Query("SELECT r.*, s.rank AS sheet_rank, s.num AS sheet_num, s.price AS sheet_price, e.price AS event_price FROM reservations r INNER JOIN sheets s ON s.id = r.sheet_id INNER JOIN events e ON e.id = r.event_id WHERE r.event_id = ? ORDER BY reserved_at ASC", event.ID)
		if err != nil {
			return err
		}
		defer rows.Close()

		var reports []Report
		for rows.Next() {
			var reservation Reservation
			var sheet Sheet
			if err := rows.Scan(&reservation.ID, &reservation.EventID, &reservation.SheetID, &reservation.UserID, &reservation.ReservedAt, &reservation.CanceledAt, &sheet.Rank, &sheet.Num, &sheet.Price, &event.Price); err != nil {
				return err
			}
			report := Report{
				ReservationID: reservation.ID,
				EventID:       event.ID,
				Rank:          sheet.Rank,
				Num:           sheet.Num,
				UserID:        reservation.UserID,
				SoldAt:        reservation.ReservedAt.Format("2006-01-02T15:04:05.000000Z"),
				Price:         event.Price + sheet.Price,
			}
			if reservation.CanceledAt != nil {
				report.CanceledAt = reservation.CanceledAt.Format("2006-01-02T15:04:05.000000Z")
			}
			reports = append(reports, report)
		}
		return renderReportCSV(c, reports)
	}, adminLoginRequired)

	// TODO: tuning
	e.GET("/admin/api/reports/sales", func(c echo.Context) error {
		rows, err := db.Query("select SQL_BIG_RESULT r.*, s.rank as sheet_rank, s.num as sheet_num, s.price as sheet_price, e.id as event_id, e.price as event_price from reservations r inner join sheets s on s.id = r.sheet_id inner join events e on e.id = r.event_id")
		if err != nil {
			return err
		}
		defer rows.Close()

		var reports []Report
		for rows.Next() {
			var reservation Reservation
			var sheet Sheet
			var event Event
			if err := rows.Scan(&reservation.ID, &reservation.EventID, &reservation.SheetID, &reservation.UserID, &reservation.ReservedAt, &reservation.CanceledAt, &sheet.Rank, &sheet.Num, &sheet.Price, &event.ID, &event.Price); err != nil {
				return err
			}
			report := Report{
				ReservationID: reservation.ID,
				EventID:       event.ID,
				Rank:          sheet.Rank,
				Num:           sheet.Num,
				UserID:        reservation.UserID,
				SoldAt:        reservation.ReservedAt.Format("2006-01-02T15:04:05.000000Z"),
				Price:         event.Price + sheet.Price,
			}
			if reservation.CanceledAt != nil {
				report.CanceledAt = reservation.CanceledAt.Format("2006-01-02T15:04:05.000000Z")
			}
			reports = append(reports, report)
		}
		return renderReportCSV(c, reports)
	}, adminLoginRequired)

	e.Start(":8080")
}

type Report struct {
	ReservationID int64
	EventID       int64
	Rank          string
	Num           int64
	UserID        int64
	SoldAt        string
	CanceledAt    string
	Price         int64
}

func renderReportCSV(c echo.Context, reports []Report) error {
	sort.Slice(reports, func(i, j int) bool { return strings.Compare(reports[i].SoldAt, reports[j].SoldAt) < 0 })

	body := bytes.NewBufferString("reservation_id,event_id,rank,num,price,user_id,sold_at,canceled_at\n")
	for _, v := range reports {
		body.WriteString(fmt.Sprintf("%d,%d,%s,%d,%d,%d,%s,%s\n",
			v.ReservationID, v.EventID, v.Rank, v.Num, v.Price, v.UserID, v.SoldAt, v.CanceledAt))
	}

	c.Response().Header().Set("Content-Type", `text/csv; charset=UTF-8`)
	c.Response().Header().Set("Content-Disposition", `attachment; filename="report.csv"`)
	_, err := io.Copy(c.Response(), body)
	return err
}

func resError(c echo.Context, e string, status int) error {
	if e == "" {
		e = "unknown"
	}
	if status < 100 {
		status = 500
	}
	return c.JSON(status, map[string]string{"error": e})
}

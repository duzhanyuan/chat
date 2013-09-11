package main

import (
	"database/sql"
	"github.com/go-sql-driver/mysql"
	"sync"
	"time"
)

var db *sql.DB
var dblock = sync.RWMutex{}

func initDatabase(dbtype string, dbdsn string) {
	dblock.Lock()
	defer dblock.Unlock()

	var err error
	db, err = sql.Open(dbtype, dbdsn)
	err2 := db.Ping()
	if err != nil || err2 != nil {
		B("Could not connect to database: ", err, err2)
		time.Sleep(time.Second)
		initDatabase(dbtype, dbdsn)
	}
}

func insertChatEvent(userid Userid, event string, data *EventDataOut, retry bool) {
	dblock.Lock()

	insertstatement, err := db.Prepare(`
		INSERT INTO chatlog
		SET
			userid       = ?,
			targetuserid = ?,
			event        = ?,
			data         = ?,
			timestamp    = ?
	`)

	if err != nil {
		B("Unable to create insert statement: ", err)
		dblock.Unlock()
		if retry {
			insertChatEvent(userid, event, data, false)
		}
		return
	}
	defer insertstatement.Close()

	targetuserid := &sql.NullInt64{}
	if data.Targetuserid != 0 {
		targetuserid.Int64 = int64(data.Targetuserid)
		targetuserid.Valid = true
	}

	d := &sql.NullString{}
	if len(data.Data) != 0 {
		d.String = data.Data
		d.Valid = true
	}

	// the timestamp is milisecond precision
	ts := time.Unix(data.Timestamp/1000, 0).UTC()
	_, err = insertstatement.Exec(userid, targetuserid, event, d, ts)
	dblock.Unlock()
	if err != nil {
		D("Unable to insert event: ", err)
		if retry {
			insertChatEvent(userid, event, data, false)
		}
	}
}

func insertBan(uid Userid, targetuid Userid, ban *BanIn, ip string, retry bool) {
	dblock.Lock()

	banstatement, err := db.Prepare(`
		INSERT INTO bans
		SET
			userid         = ?,
			targetuserid   = ?,
			ipaddress      = ?,
			reason         = ?,
			starttimestamp = ?,
			endtimestamp   = ?
	`)

	if err != nil {
		B("Unable to create ban statement: ", err)
		dblock.Unlock()
		if retry {
			insertBan(uid, targetuid, ban, ip, false)
		}
		return
	}
	defer banstatement.Close()

	ipaddress := &sql.NullString{}
	if ban.BanIP && len(ip) != 0 {
		ipaddress.String = ip
		ipaddress.Valid = true
	}
	starttimestamp := time.Now().UTC()

	endtimestamp := &mysql.NullTime{}
	if !ban.Ispermanent {
		endtimestamp.Time = starttimestamp.Add(time.Duration(ban.Duration))
		endtimestamp.Valid = true
	}

	_, err = banstatement.Exec(uid, targetuid, ipaddress, ban.Reason, starttimestamp, endtimestamp)
	dblock.Unlock()
	if err != nil {
		D("Unable to insert ban: ", err)
		if retry {
			insertBan(uid, targetuid, ban, ip, false)
		}
	}
}

func getBans(f func(Userid, sql.NullString, mysql.NullTime)) {
	dblock.Lock()
	defer dblock.Unlock()

	rows, err := db.Query(`
		SELECT
			targetuserid,
			ipaddress,
			endtimestamp
		FROM bans
		WHERE
			endtimestamp IS NULL OR
			endtimestamp > NOW()
		GROUP BY targetuserid, ipaddress
	`)

	if err != nil {
		B("Unable to get active bans: ", err)
		return
	}

	defer rows.Close()
	for rows.Next() {
		var uid Userid
		var ipaddress sql.NullString
		var endtimestamp mysql.NullTime
		err = rows.Scan(&uid, &ipaddress, &endtimestamp)

		if err != nil {
			B("Unable to scan bans row: ", err)
			continue
		}

		f(uid, ipaddress, endtimestamp)
	}
}

func deleteBan(targetuid Userid, retry bool) {
	dblock.Lock()

	unbanstatement, err := db.Prepare(`
		UPDATE bans
		SET endtimestamp = NOW()
		WHERE
			targetuserid = ? AND
			(
				endtimestamp IS NULL OR
				endtimestamp > NOW()
			)
	`)

	if err != nil {
		B("Unable to create unban statement: ", err)
		dblock.Unlock()
		if retry {
			deleteBan(targetuid, false)
		}
		return
	}
	defer unbanstatement.Close()

	_, err = unbanstatement.Exec(targetuid)
	dblock.Unlock()
	if err != nil {
		D("Unable to unban: ", err)
		if retry {
			deleteBan(targetuid, false)
		}
	}
}

func getUsers(f func(Userid, string, bool)) {
	dblock.Lock()
	defer dblock.Unlock()

	rows, err := db.Query(`
		SELECT DISTINCT
			u.userId,
			u.username,
			IF(IFNULL(f.featureId, 0) >= 1, 1, 0) AS protected
		FROM dfl_users AS u
		LEFT JOIN dfl_users_features AS f ON (
			f.userId = u.userId AND
			featureId = (SELECT featureId FROM dfl_features WHERE featureName IN("protected", "admin") LIMIT 1)
		)
	`)

	if err != nil {
		B("Unable to load userids:", err)
		return
	}

	defer rows.Close()
	for rows.Next() {
		var uid Userid
		var nick string
		var protected bool

		err = rows.Scan(&uid, &nick, &protected)
		if err != nil {
			B("Unable to scan row: ", err)
			continue
		}

		f(uid, nick, protected)
	}
}

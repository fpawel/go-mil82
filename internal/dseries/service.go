package dseries

import (
	"encoding/binary"
	"github.com/fpawel/comm/modbus"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ChartsSvc struct{}

type YearMonth struct {
	Year  int `db:"created_at_year"`
	Month int `db:"created_at_month"`
}

func (_ *ChartsSvc) YearsMonths(_ struct{}, r *[]YearMonth) error {
	if err := db.Select(r, `
SELECT DISTINCT created_at_year, created_at_month 
FROM bucket_time 
ORDER BY created_at_year DESC, created_at_month DESC`); err != nil {
		panic(err)
	}
	return nil
}

func (_ *ChartsSvc) BucketsOfYearMonth(x YearMonth, r *[]ChartsBucket) error {
	var xs []bucket
	if err := db.Select(&xs, `
SELECT * FROM bucket_time
WHERE created_at_year = ?
  AND created_at_month = ?
ORDER BY created_at`, x.Year, x.Month); err != nil {
		panic(err)
	}
	for _, x := range xs {
		*r = append(*r, ChartsBucket{
			CreatedAt: TimeDelphi{
				Year:   x.CreatedAtYear,
				Month:  time.Month(x.CreatedAtMonth),
				Day:    x.CreatedAtDay,
				Hour:   x.CreatedAtHour,
				Minute: x.CreatedAtMinute,
			},
			UpdatedAt: TimeDelphi{
				Year:   x.UpdatedAtYear,
				Month:  time.Month(x.UpdatedAtMonth),
				Day:    x.UpdatedAtDay,
				Hour:   x.UpdatedAtHour,
				Minute: x.UpdatedAtMinute,
			},
			BucketID: x.BucketID,
			Name:     x.Name,
			IsLast:   x.IsLast,
		})
	}

	return nil
}

func (_ *ChartsSvc) DeletePoints(r DeletePointsRequest, rowsAffected *int64) error {

	lastBucket, hasBuckets := lastBucket()
	if !hasBuckets {
		return nil
	}

	if r.BucketID == 0 {
		r.BucketID = lastBucket.BucketID
	}
	if r.BucketID == lastBucket.BucketID {
		muPoints.Lock()
		n := 0
		for _, x := range currentPoints {
			f := x.Addr == x.Addr && x.Var == x.Var &&
				x.StoredAt.After(r.TimeMinimum.Time()) && x.StoredAt.Before(r.TimeMaximum.Time()) &&
				x.Value >= r.ValueMinimum &&
				x.Value <= r.ValueMaximum
			if f {
				currentPoints[n] = x
				n++
			}
		}
		currentPoints = currentPoints[:n]
		muPoints.Unlock()
	}

	var addresses, vars []string
	for _, addr := range r.Addresses {
		addresses = append(addresses, strconv.Itoa(int(addr)))
	}
	for _, Var := range r.Vars {
		vars = append(addresses, strconv.Itoa(int(Var)))
	}

	const timeFormat = "2006-01-02 15:04:05.000"
	var err error
	*rowsAffected, err = db.MustExec(
		`
DELETE FROM series 
WHERE bucket_id = ? AND      
      value >= ? AND 
      value <= ? AND 
      stored_at >= julianday(?) AND 
      stored_at <= julianday(?) `+
			"AND addr IN ("+strings.Join(addresses, ",")+")"+
			"AND var IN ("+strings.Join(vars, ",")+")", r.BucketID,
		r.ValueMinimum, r.ValueMaximum,
		r.TimeMinimum.Time().Format(timeFormat),
		r.TimeMaximum.Time().Format(timeFormat)).RowsAffected()
	if err != nil {
		panic(err)
	}
	return nil
}

func HandleRequestChart(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Accept", "application/octet-stream")
	bucketID, _ := strconv.ParseInt(r.URL.Query().Get("bucket"), 10, 64)
	writePointsResponse(w, bucketID)
}

func writePointsResponse(w io.Writer, bucketID int64) {

	var points []point3

	if err := db.Select(&points, `
SELECT addr, var, value, year, month, day, hour, minute, second, millisecond 
FROM series_time 
WHERE bucket_id = ?`, bucketID); err != nil {
		panic(err)
	}

	if b, f := lastBucket(); f && b.BucketID == bucketID {
		var points3 []point3
		muPoints.Lock()
		for _, p := range currentPoints {
			points3 = append(points3, p.point3())
		}
		muPoints.Unlock()
		points = append(points3, points...)
	}

	write := func(n interface{}) {
		if err := binary.Write(w, binary.LittleEndian, n); err != nil {
			panic(err)
		}
	}
	write(uint64(len(points)))
	for _, x := range points {
		write(byte(x.Addr))
		write(uint16(x.Var))
		write(uint16(x.Year))
		write(byte(x.Month))
		write(byte(x.Day))
		write(byte(x.Hour))
		write(byte(x.Minute))
		write(byte(x.Second))
		write(uint16(x.Millisecond))
		write(x.Value)
	}
}

type DeletePointsRequest struct {
	BucketID  int64
	Addresses []modbus.Addr
	Vars      []modbus.Var
	ValueMinimum,
	ValueMaximum float64
	TimeMinimum,
	TimeMaximum TimeDelphi
}

package main

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// rowsPerStatement は1つの INSERT 文に載せる行数。
// 1行1文にすると30万行の読み込みが桁違いに遅くなる。
const rowsPerStatement = 1000

const mysqlTimeLayout = "2006-01-02 15:04:05.000000"

// WriteSQL は生成データを dir 配下の SQL ファイルへ書き出す。
func WriteSQL(dir string, ds *Dataset) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	type job struct {
		name string
		fn   func(*bufio.Writer, *Dataset) error
	}
	for _, j := range []job{
		{"91_users.sql", writeUsers},
		{"92_auctions.sql", writeAuctions},
		{"93_bids.sql", writeBids},
		{"94_notifications.sql", writeNotifications},
	} {
		f, err := os.Create(filepath.Join(dir, j.name))
		if err != nil {
			return err
		}
		w := bufio.NewWriterSize(f, 1<<20)
		if err := j.fn(w, ds); err != nil {
			f.Close()
			return err
		}
		if err := w.Flush(); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}

// writeChunked は n 行を rowsPerStatement ごとに区切って INSERT 文を書く。
// row(i) は i 番目の行の "(...)" 部分を返す。
func writeChunked(w *bufio.Writer, header string, n int, row func(i int) string) error {
	return writeChunkedN(w, header, n, rowsPerStatement, row)
}

// writeChunkedN は1文あたりの行数を指定できる writeChunked。
// アイコンのように1行が大きいテーブルで行数を下げるために使う。
func writeChunkedN(w *bufio.Writer, header string, n, perStatement int, row func(i int) string) error {
	if n == 0 {
		// 空でも構文として成立するファイルにしておく(mysql が読んでも何もしない)
		_, err := fmt.Fprintf(w, "-- no rows\nSELECT 1;\n")
		return err
	}
	for start := 0; start < n; start += perStatement {
		end := start + perStatement
		if end > n {
			end = n
		}
		if _, err := fmt.Fprint(w, header); err != nil {
			return err
		}
		for i := start; i < end; i++ {
			sep := ",\n"
			if i == end-1 {
				sep = ";\n"
			}
			if _, err := fmt.Fprint(w, row(i), sep); err != nil {
				return err
			}
		}
	}
	return nil
}

// usersPerStatement は users の INSERT 1文に載せる行数。
//
// アイコンが入ると1行が数百バイト〜数KB(バイナリが16進で2倍)になるため、
// 他テーブルの rowsPerStatement(1000)のままだと1文が肥大する。
// 100行なら1文あたり数百KBに収まり、MySQL の max_allowed_packet に余裕を持てる。
const usersPerStatement = 100

func writeUsers(w *bufio.Writer, ds *Dataset) error {
	return writeChunkedN(w, "INSERT INTO users (id, name, password_hash, icon) VALUES\n",
		len(ds.Users), usersPerStatement, func(i int) string {
			u := ds.Users[i]
			return fmt.Sprintf("(%d, '%s', '%s', %s)",
				u.ID, u.Name, GeneratedPasswordHash, iconSQL(u.Icon))
		})
}

// iconSQL はアイコンを MySQL の16進リテラルへ変換する。nil は NULL。
func iconSQL(b []byte) string {
	if b == nil {
		return "NULL"
	}
	return "0x" + hex.EncodeToString(b)
}

func nullInt64SQL(v *int64) string {
	if v == nil {
		return "NULL"
	}
	return fmt.Sprintf("%d", *v)
}

func writeAuctions(w *bufio.Writer, ds *Dataset) error {
	header := "INSERT INTO auctions (id, seller_id, category_id, title, description, starting_price, starts_at, ends_at, status, winner_id, winning_price) VALUES\n"
	return writeChunked(w, header, len(ds.Auctions), func(i int) string {
		a := ds.Auctions[i]
		return fmt.Sprintf("(%d, %d, %d, '%s', '%s', %d, '%s', '%s', '%s', %s, %s)",
			a.ID, a.SellerID, a.CategoryID, a.Title, a.Description, a.StartingPrice,
			a.StartsAt.Format(mysqlTimeLayout), a.EndsAt.Format(mysqlTimeLayout), a.Status,
			nullInt64SQL(a.WinnerID), nullInt64SQL(a.WinningPrice))
	})
}

func writeBids(w *bufio.Writer, ds *Dataset) error {
	header := "INSERT INTO bids (id, auction_id, user_id, amount, created_at) VALUES\n"
	return writeChunked(w, header, len(ds.Bids), func(i int) string {
		b := ds.Bids[i]
		return fmt.Sprintf("(%d, %d, %d, %d, '%s')",
			b.ID, b.AuctionID, b.UserID, b.Amount, b.CreatedAt.Format(mysqlTimeLayout))
	})
}

func writeNotifications(w *bufio.Writer, ds *Dataset) error {
	header := "INSERT INTO notifications (id, user_id, type, auction_id, message, is_read, created_at) VALUES\n"
	return writeChunked(w, header, len(ds.Notifications), func(i int) string {
		n := ds.Notifications[i]
		return fmt.Sprintf("(%d, %d, '%s', %d, '%s', 0, '%s')",
			n.ID, n.UserID, n.Type, n.AuctionID, n.Message, n.CreatedAt.Format(mysqlTimeLayout))
	})
}

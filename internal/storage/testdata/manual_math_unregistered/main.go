// Fresh-process test helper. Deliberately do not import internal/storage or any
// package that registers MANUAL functions. No network or service composition.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	db, err := sql.Open("sqlite", os.Args[1])
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO math_external_probe VALUES('1')`); err == nil || !(strings.Contains(err.Error(), "no such function: manual_u256_cmp") || strings.Contains(err.Error(), "unknown function: manual_u256_cmp")) {
		panic(fmt.Sprintf("unregistered schema write: %v", err))
	}
	var rows int
	if err = db.QueryRow(`SELECT COUNT(*) FROM math_external_probe`).Scan(&rows); err != nil || rows != 0 {
		panic(fmt.Sprintf("partial external write: rows=%d err=%v", rows, err))
	}
	fmt.Println("unregistered write rejected; rows=0")
}

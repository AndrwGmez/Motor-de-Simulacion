package datos

import "database/sql"

func guardarLiquidacion(db *sql.DB, total float64) error {
	_, err := db.Exec("insert into liquidaciones values ($1)", total)
	return err
}

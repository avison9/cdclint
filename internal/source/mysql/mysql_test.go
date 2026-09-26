package mysql

import (
	"strings"
	"testing"

	"github.com/avison9/cdclint/internal/model"
	"github.com/avison9/cdclint/internal/source"
)

func columns(t *model.Table) string {
	var names []string
	for _, c := range t.Columns {
		names = append(names, c.Name)
	}
	return strings.Join(names, ",")
}

func TestReadsTheSchemaMigrationsLeaveBehind(t *testing.T) {
	files := []source.NamedFile{
		{Path: "V1__init.sql", Text: "# Flyway style\n" +
			"CREATE TABLE `orders` (\n" +
			"  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,\n" +
			"  `note` VARCHAR(40) DEFAULT 'it\\'s, fine' COMMENT 'a, b',\n" +
			"  spatial POINT NOT NULL SRID 4326,\n" +
			"  `status` ENUM('new','paid') NOT NULL DEFAULT 'new',\n" +
			"  PRIMARY KEY (`id`),\n" +
			"  KEY `idx_status` (`status`),\n" +
			"  SPATIAL INDEX (spatial),\n" +
			"  CONSTRAINT `fk` FOREIGN KEY (`id`) REFERENCES other (`id`)\n" +
			") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;\n" +
			"CREATE TABLE IF NOT EXISTS orders (ignored INT);\n" +
			"CREATE TEMPORARY TABLE scratch (x INT);\n"},
		{Path: "V2__alter.sql", Text: "ALTER TABLE orders\n" +
			"  ADD COLUMN currency CHAR(3) NOT NULL DEFAULT 'EUR' AFTER note,\n" +
			"  ADD (paid_at DATETIME(6), refunded TINYINT(1)),\n" +
			"  ADD INDEX idx_paid (paid_at),\n" +
			"  DROP INDEX idx_status,\n" +
			"  CHANGE COLUMN note memo TEXT,\n" +
			"  MODIFY status VARCHAR(10);\n" +
			"ALTER TABLE orders ADD first_col INT FIRST, DROP COLUMN spatial;\n" +
			"ALTER TABLE orders RENAME COLUMN refunded TO is_refunded;\n" +
			"CREATE TABLE orders_archive LIKE orders;\n" +
			"RENAME TABLE orders_archive TO archive.orders_2025;\n" +
			"USE reporting;\n" +
			"CREATE TABLE daily (day DATE PRIMARY KEY, total DECIMAL(12,2));\n" +
			"DROP TABLE IF EXISTS shop.gone, daily;\n"},
	}
	src, err := ReadFiles(files, "shop")
	if err != nil {
		t.Fatal(err)
	}
	orders := src.Table("shop", "orders")
	if orders == nil {
		t.Fatal("shop.orders missing")
	}
	if got, want := columns(orders), "first_col,id,memo,currency,status,paid_at,is_refunded"; got != want {
		t.Errorf("shop.orders columns = %s, want %s", got, want)
	}
	if got := orders.Column("id").Type; got != "BIGINT UNSIGNED" {
		t.Errorf("id type = %q", got)
	}
	if got := orders.Column("status").Type; got != "VARCHAR(10)" {
		t.Errorf("status type after MODIFY = %q", got)
	}
	if got := strings.Join(orders.PrimaryKey, ","); got != "id" {
		t.Errorf("primary key = %q", got)
	}
	if orders.ReplicaIdentity != "" {
		t.Errorf("replica identity = %q, want empty for MySQL", orders.ReplicaIdentity)
	}
	// LIKE copied orders as it stood then, and RENAME TABLE moved it to
	// another database.
	moved := src.Table("archive", "orders_2025")
	if moved == nil {
		t.Fatal("archive.orders_2025 missing")
	}
	if got, want := columns(moved), "first_col,id,memo,currency,status,paid_at,is_refunded"; got != want {
		t.Errorf("archive.orders_2025 columns = %s, want %s", got, want)
	}
	if src.Table("shop", "orders_archive") != nil || src.Table("shop", "scratch") != nil {
		t.Error("renamed or temporary table still present")
	}
	if src.Table("reporting", "daily") != nil {
		t.Error("reporting.daily was dropped by DROP TABLE after USE reporting")
	}
}

func TestANameGluedToAMultiLineColumnList(t *testing.T) {
	// Mattermost's 000016_create_reactions: the paren opens a list that
	// runs over several lines.
	src, err := ReadFiles([]source.NamedFile{{Path: "V1.sql",
		Text: "CREATE TABLE IF NOT EXISTS Reactions(\n    UserId varchar(26) NOT NULL,\n    PostId varchar(26) NOT NULL\n);"}}, "mm")
	if err != nil {
		t.Fatal(err)
	}
	if r := src.Table("mm", "Reactions"); r == nil || columns(r) != "UserId,PostId" {
		t.Fatalf("mm.Reactions = %+v", r)
	}
}

func TestDownMigrationsAreLeftOut(t *testing.T) {
	// Mattermost v10.11.0's 000092 down file drops a column of another
	// table; golang-migrate never runs it forward, and neither does this.
	files := []source.NamedFile{
		{Path: "000016_create_reactions.up.sql", Text: "CREATE TABLE Reactions (UserId varchar(26), CreateAt bigint);"},
		{Path: "000092_add_createat_to_teammembers.down.sql", Text: "ALTER TABLE Reactions DROP COLUMN CreateAt;"},
		{Path: "000093_goose.sql", Text: "-- +goose Up\nALTER TABLE Reactions ADD COLUMN EmojiName varchar(64);\n-- +goose Down\nDROP TABLE Reactions;\n"},
	}
	src, err := ReadFiles(files, "mm")
	if err != nil {
		t.Fatal(err)
	}
	if r := src.Table("mm", "Reactions"); r == nil || columns(r) != "UserId,CreateAt,EmojiName" {
		t.Fatalf("mm.Reactions = %+v", r)
	}
}

func TestColumnsAnAlterAddsAreOnTheirOwnLines(t *testing.T) {
	text := "CREATE TABLE orders (id INT);\n" +
		"ALTER TABLE `orders`\n" +
		"  ADD COLUMN `currency` CHAR(3) AFTER id,\n" +
		"  ADD (\n" +
		"    paid_at DATETIME,\n" +
		"    orders_total DECIMAL(12,2)\n" +
		"  );\n"
	src, err := ReadFiles([]source.NamedFile{{Path: "V2.sql", Text: text}}, "shop")
	if err != nil {
		t.Fatal(err)
	}
	o := src.Table("shop", "orders")
	for name, want := range map[string]int{"currency": 3, "paid_at": 5, "orders_total": 6} {
		if c := o.Column(name); c == nil || c.Pos.Line != want {
			t.Errorf("%s at %+v, want line %d", name, c, want)
		}
	}
}

func TestAnUnqualifiedTableWithNoDatabaseIsAnError(t *testing.T) {
	_, err := ReadFiles([]source.NamedFile{{Path: "V1.sql", Text: "CREATE TABLE t (id INT);"}}, "")
	if err == nil || !strings.Contains(err.Error(), "database.include.list") {
		t.Fatalf("err = %v, want one naming database.include.list", err)
	}
	src, err := ReadFiles([]source.NamedFile{{Path: "V1.sql", Text: "USE app;\nCREATE TABLE t (id INT);"}}, "")
	if err != nil || src.Table("app", "t") == nil {
		t.Fatalf("USE should place the table: %v %v", src, err)
	}
}

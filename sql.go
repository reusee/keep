package main

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

func sqlInterface(
	rootAccount *Account,
	transactions []*Transaction,
) {

	dbDir := filepath.Join(os.TempDir(), fmt.Sprintf("keep-%d", rand.Int63()))
	if out, err := exec.Command("initdb", "-D", dbDir).CombinedOutput(); err != nil {
		fail("%v: %s", err, out)
	}
	pt("db dir: %s\n", dbDir)
	defer exec.Command("rm", "-rf", dbDir).Run()

	port := 10000 + rand.Intn(50000)
	conf := `
		port = ` + fmt.Sprintf("%d", port) + `
	`
	if err := ioutil.WriteFile(filepath.Join(dbDir, "postgresql.conf"), []byte(conf), 0644); err != nil {
		fail("%v", err)
	}
	c := exec.Command("postgres", "-D", dbDir)
	c.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	if err := c.Start(); err != nil {
		fail("%v", err)
	}
	defer syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	time.Sleep(time.Second)
	pt("db started\n")

	db, err := sqlx.Open("postgres", fmt.Sprintf("postgres://localhost:%d/postgres?sslmode=disable", port))
	if err != nil {
		fail("%v", err)
	}
	defer db.Close()
	tx := db.MustBegin()
	if _, err := tx.Exec(`
		CREATE TABLE entries (
			id bigserial primary key,
			transaction bigint,
			transaction_description text,
			time timestamp with time zone,
			account text[],
			currency text,
			amount numeric,
			description text
		)
		`,
	); err != nil {
		fail("%v", err)
	}
	for _, view := range views {
		if _, err := tx.Exec(view); err != nil {
			fail("%v", err)
		}
	}
	for tid, transaction := range transactions {
		for _, entry := range transaction.Entries {
			if _, err := tx.Exec(`
				INSERT INTO entries
				(
					transaction, transaction_description,
					time, account, currency, amount, description
				)
				VALUES (
					$1, $2,
					$3, $4, $5, $6::numeric, $7
				)
				`,
				tid,
				transaction.Description,
				entry.Time,
				func() (ret pq.StringArray) {
					acc := entry.Account
					for acc != rootAccount {
						ret = append(ret, acc.Name)
						acc = acc.Parent
					}
					for i := len(ret)/2 - 1; i >= 0; i-- {
						j := len(ret) - 1 - i
						ret[i], ret[j] = ret[j], ret[i]
					}
					return
				}(),
				entry.Currency,
				entry.Amount.FloatString(3),
				entry.Description,
			); err != nil {
				fail("%v", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		fail("%v", err)
	}
	pt("data loaded\n")

	sigs := make(chan os.Signal)
	go func() {
		for {
			<-sigs
		}
	}()
	signal.Notify(sigs, os.Interrupt)

	psql := exec.Command("psql", fmt.Sprintf("postgres://localhost:%d/postgres", port))
	psql.Stdout = os.Stdout
	psql.Stdin = os.Stdin
	psql.Stderr = os.Stderr
	if err := psql.Run(); err != nil {
		fail("%v", err)
	}
}

var views = []string{
	// props
	`
	create view props as 
	select distinct on (time, transaction)
	time, transaction_description
	from entries
	where account[1] = '支出'
	and account[2] in (
		'数码',
		'物品',
		'衣物服饰',
		'消耗品',
		'保健品',
		'书籍',
		'药物',
		'性用品'
	)
	order by time desc, transaction
	`,

	// monthly_expense
	`
	create view monthly_expense as 
	select 
	to_char(date_trunc('month', time), 'YYYY-MM') as month, 
	currency, 
	sum(amount),
	(
		select string_agg(
			account || currency || amount::text,
			E'\n'
		) from (
			select account[2] as account, currency, sum(amount) as amount
			from unnest(array_agg(id)) as id
			join entries e2 using (id)
			group by account[2], currency
			order by amount desc
		) t0
	)
	from entries 
	where account[1] = '支出' 
	group by date_trunc('month', time), currency 
	order by month desc, currency asc
	`,

	// monthly_income
	`
	create view monthly_income as 
	select 
	to_char(date_trunc('month', time), 'YYYY-MM') as month, 
	currency, 
	sum(amount),
	(
		select string_agg(
			account || currency || amount::text,
			E'\n'
		) from (
			select account[2] as account, currency, sum(amount) as amount
			from unnest(array_agg(id)) as id
			join entries e2 using (id)
			group by account[2], currency
			order by amount asc
		) t0
	)
	from entries 
	where account[1] = '收入' 
	group by date_trunc('month', time), currency 
	order by month desc, currency asc
	`,

	//
}

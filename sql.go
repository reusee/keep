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
			date timestamp with time zone,
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
					date, account, currency, amount, description
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
	select distinct on (date, transaction)
	date, transaction_description
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
	order by date desc, transaction
	`,

	// consumables
	`
	create view consumables as 
	select distinct on (date, transaction)
	date, transaction_description
	from entries
	where account[1] = '支出'
	and account[2] in (
		'饮食',
		'消耗品',
		'药物',
		'保健品'
	)
	order by date desc, transaction
	`,

	// monthly
	"create view monthly as" + intervalStat("date_trunc('month', date)"),

	// yearly
	"create view yearly as" + intervalStat("date_trunc('year', date)"),

	// seasonally
	"create view seasonally as" + intervalStat("date_trunc('year', date) + interval '3 month' * (extract(month from date)::int / 3)"),

	// this year expenses
	`
	create view this_year_expenses as
	select 
	currency, sum(amount), account[2], 
	jsonb_pretty(jsonb_agg(
			to_char(date, 'YYYY-MM-DD') 
			|| ' ' 
			|| currency 
			|| amount::numeric(10,2)
			|| ' ' 
			|| transaction_description 
			order by amount desc, date desc
	)) 
	from entries
	where extract(year from date) = extract(year from now())
	and account[1] = '支出' 
	group by account[2],currency 
	order by sum desc
	`,

	//
}

func intervalStat(groupBy string) string {
	return `
	select 
	to_char(` + groupBy + `, 'YYYY-MM') as span,

	COALESCE(
		'支出' || currency || (sum(amount) filter (where account[1] = '支出'))::numeric(20,2)::text 
		|| E'：\n'
		|| (
			select string_agg(
				currency || amount::numeric(20,2)::text || ' ' || account,
				E'\n'
				order by amount desc
			) from (
				select account[2] as account, currency, sum(amount) as amount
				from unnest(
					array_agg(id) filter (where account[1] = '支出')
				) as id
				join entries e2 using (id)
				group by account[2], currency
			) t0
		),
		'-'
	) || E'\n' as expenses,

	COALESCE(
		'收入' || currency || (sum(amount) filter (where account[1] = '收入'))::numeric(20,2)::text 
		|| E'：\n'
		|| (
			select string_agg(
				currency || amount::numeric(20, 2)::text || ' ' || account,
				E'\n'
				order by amount asc
			) from (
				select account[2] as account, currency, sum(amount) as amount
				from unnest(
					array_agg(id) filter (where account[1] = '收入')
				) as id
				join entries e2 using (id)
				group by account[2], currency
			) t0
		),
		'-'
	) || E'\n' as income,

	COALESCE(
		'净资产' || currency || (
			-sum(amount) filter (where account[1] = '收入')
			-
			sum(amount) filter (where account[1] = '支出')
		)::numeric(20,2)::text,
		'-'
	) || E'\n' as net_income,

	COALESCE(
		E'资产：\n' || (
			select string_agg(
				currency 
				|| amount::numeric(20, 2)::text || ' ' || account
				|| E'\n'
				|| '= 增' || pos_amount::numeric(20, 2)::text
				|| ' 减' || neg_amount::numeric(20, 2)::text,
				E'\n'
				order by amount desc
			) from (
				select account[2] as account, currency, sum(amount) as amount,
				COALESCE(sum(amount) filter (where amount >= 0), 0) as pos_amount,
				COALESCE(-sum(amount) filter (where amount < 0), 0) as neg_amount
				from unnest(
					array_agg(id) filter (where account[1] = '资产')
				) as id
				join entries e2 using (id)
				group by account[2], currency
			) t0
		),
		'-'
	) || E'\n' as equity,

	COALESCE(
		E'负债：\n' || (
			select string_agg(
				currency 
				|| amount::numeric(20, 2)::text || ' ' || account
				|| E'\n'
				|| '= 还' || pos_amount::numeric(20, 2)::text
				|| ' 借' || neg_amount::numeric(20, 2)::text,
				E'\n'
				order by amount asc
			) from (
				select account[2] as account, currency, sum(amount) as amount,
				COALESCE(sum(amount) filter (where amount >= 0), 0) as pos_amount,
				COALESCE(-sum(amount) filter (where amount < 0), 0) as neg_amount
				from unnest(
					array_agg(id) filter (where account[1] = '负债')
				) as id
				join entries e2 using (id)
				group by account[2], currency
			) t0
		),
		'-'
	) || E'\n' as liability

	from
	entries

	group by ` + groupBy + `, currency
	order by span desc, currency asc
	`
}

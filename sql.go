package main

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

func sqlInterface(
	rootAccount *Account,
	transactions []*Transaction,
) {

	execCommand := func(name string, args ...string) *exec.Cmd {
		if isRoot {
			as := []string{
				"-u",
				"postgres",
			}
			as = append(as, name)
			as = append(as, args...)
			return exec.Command("sudo", as...)
		}
		return exec.Command(name, args...)
	}

	dbDir := filepath.Join(os.TempDir(), fmt.Sprintf("keep-%d", rand.Int63()))
	out, err := execCommand(
		"initdb",
		"-D", dbDir,
		"--auth", "trust",
		"--username", "foo",
	).CombinedOutput()
	ce(we(err, "%s", out))
	pt("db dir: %s\n", dbDir)
	defer execCommand("rm", "-rf", dbDir).Run()

	port := 10000 + rand.Intn(50000)
	conf := `
		port = ` + fmt.Sprintf("%d", port) + `
		unix_socket_directories = '/tmp'
	`
	err = ioutil.WriteFile(filepath.Join(dbDir, "postgresql.conf"), []byte(conf), 0644)
	ce(err)
	c := execCommand("postgres", "-D", dbDir)
	c.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	ce(c.Start())
	defer syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	time.Sleep(time.Second)
	pt("db started at port %d\n", port)

	db, err := sqlx.Open("postgres", fmt.Sprintf("postgres://foo@127.0.0.1:%d/template1?sslmode=disable", port))
	ce(err)
	defer db.Close()
	tx := db.MustBegin()
	_, err = tx.Exec(`
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
	)
	ce(err)

	for _, view := range views {
		_, err := tx.Exec(view)
		ce(err)
	}

	stmt, err := tx.Prepare(pq.CopyIn("entries",
		"transaction", "transaction_description",
		"date", "account",
		"currency", "amount",
		"description",
	))
	if err != nil {
		panic(err)
	}
	for tid, transaction := range transactions {
		for _, entry := range transaction.Entries {
			if _, err := stmt.Exec(
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
				panic(err)
			}
		}
	}
	if _, err := stmt.Exec(); err != nil {
		panic(err)
	}
	if err := stmt.Close(); err != nil {
		panic(err)
	}
	ce(tx.Commit())
	pt("data loaded\n")

	sigs := make(chan os.Signal)
	go func() {
		for {
			<-sigs
		}
	}()
	signal.Notify(sigs, os.Interrupt)

	psql := exec.Command("psql", fmt.Sprintf("postgres://foo@127.0.0.1:%d/template1", port))
	psql.Stdout = os.Stdout
	psql.Stdin = os.Stdin
	psql.Stderr = os.Stderr
	ce(psql.Run())
}

var views = []string{
	// things
	`
	create view things as 
	select * from (
		select max(date) as date, max(description) as description, max(kinds) as kinds
		,jsonb_object_agg(currency, amount) as amount
		from (
			select 
			max(date) as date
			,transaction
			,max(transaction_description) as description
			,array_agg(account[2]) as kinds
			,currency
			,sum(amount) as amount
			from entries
			where account[1] = '支出'
			and account[2] in (
				''
				` + func() string {
		var b strings.Builder
		for kind := range itemKinds {
			b.WriteString(",'" + kind + "'")
		}
		for kind := range consumableKinds {
			b.WriteString(",'" + kind + "'")
		}
		return b.String()
	}() + `
			)
			group by transaction, currency
		) t0
		group by transaction
	) t1
	order by date desc, description
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

	// yearly
	"create view yearly as" + intervalStat("date_trunc('year', date)"),

	// seasonally
	"create view seasonally as" + intervalStat("date_trunc('year', date) + interval '3 month' * (extract(month from date)::int / 3)"),

	// monthly
	"create view monthly as" + intervalStat("date_trunc('month', date)"),

	// weekly
	"create view weekly as" + intervalStat("date_trunc('week', date)"),

	// daily
	"create view daily as" + intervalStat("date_trunc('day', date)"),

	// this year expenses
	`
	create view yearly_expenses as
	select 
	extract(year from date) as year, currency, sum(amount), account[2], 
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
	where true
	and account[1] = '支出' 
	group by extract(year from date), account[2], currency 
	order by extract(year from date) desc, sum desc
	`,

	// balance sheet
	`
	create view balance_sheet as
	select

	(
		select string_agg(currency || amount, E'\n') from (
			select sum(amount) as amount, currency
			from entries
			where account[1] = '资产'
			group by currency
		) t0
		where amount <> 0
	)::text
	|| E'\n-----\n'
	|| (
		select string_agg(
			kind || ' ' || currency || amount, E'\n'
			order by amount desc
		) 
		from (
			select sum(amount) as amount, currency, account[2] as kind
			from entries
			where account[1] = '资产'
			group by currency, account[2]
		) t0
		where amount <> 0
	)
	AS 资产

	,(
		select string_agg(currency || amount, E'\n') from (
			select sum(amount) as amount, currency
			from entries
			where account[1] = '负债'
			group by currency
		) t0
		where amount <> 0
	)::text
	|| E'\n-----\n'
	|| (
		select string_agg(
			kind || ' ' || currency || amount, E'\n'
			order by amount asc
		) 
		from (
			select sum(amount) as amount, currency, account[2] as kind
			from entries
			where account[1] = '负债'
			group by currency, account[2]
		) t0
		where amount <> 0
	)
	AS 负债

	,(
		select string_agg(currency || amount, E'\n') from (
			select sum(amount) + (
				select sum(amount) 
				from entries e
				where account[1] = '负债'
				and currency = entries.currency
			) as amount, currency
			from entries
			where account[1] = '资产'
			group by currency
		) t0
		where amount <> 0
	)::text
	AS 净资产

	,(
		select string_agg(currency || amount, E'\n') from (
			select sum(amount) as amount, currency
			from entries
			where account[1] = '负债'
			and date < now() + interval '1 year'
			group by currency
		) t0
		where amount <> 0
	)::text
	|| E'\n-----\n'
	|| (
		select string_agg(
			kind 
			|| ' ' || currency || amount
			,E'\n'
			order by amount asc
		) 
		from (
			select sum(amount) as amount, currency, account[2] as kind
			from entries
			where account[1] = '负债'
			and date < now() + interval '1 year'
			group by currency, account[2]
		) t0
		where amount <> 0
	)
	|| E'\n-----\n'
	|| (
		select string_agg(
			to_char(month, 'YYYY-MM-DD')
			|| ' ' || currency || amount
			,E'\n'
			order by month asc
		)  filter (
			where month >= date_trunc('month', now())
		)
		from (
			select sum(amount) as amount, currency, date_trunc('month', date) as month
			from entries
			where account[1] = '负债'
			and date < now() + interval '1 year'
			group by date_trunc('month', date), currency
		) t0
		where amount <> 0
	)
	AS 一年负债

	,(
		select string_agg(currency || amount, E'\n') from (
			select sum(amount) + (
				select sum(amount) 
				from entries e
				where account[1] = '负债'
				and currency = entries.currency
				and date < now() + interval '1 year'
			) as amount, currency
			from entries
			where account[1] = '资产'
			group by currency
		) t0
		where amount <> 0
	)::text
	AS 一年净资产

	` + func() string {
		cond := `
		and true in (
			false
			,account[1] = '资产' and account[2] = '工行'
			,account[1] = '资产' and account[2] = '建行'
			,account[1] = '资产' and account[2] = '中信'
			,account[1] = '资产' and account[2] = '兴业'
			,account[1] = '资产' and account[2] = '现金'
			,account[1] = '资产' and account[2] = '微信钱包'
			,account[1] = '资产' and account[2] = '京东金库'
		)
		`
		return `
		,(
			select string_agg(currency || amount, E'\n') from (
				select sum(amount) as amount, currency
				from entries
				where true
				` + cond + `
				group by currency
			) t0
			where amount <> 0
		)::text
		|| E'\n-----\n'
		|| (
			select string_agg(
				kind 
				|| ' ' || currency || amount, E'\n'
				order by amount desc
			) 
			from (
				select sum(amount) as amount, currency, account[2] as kind
				from entries
				where true
				` + cond + `
				group by currency, account[2]
			) t0
			where amount <> 0
		)
		AS "T+0流动资产"
		`
	}() + `

	,(
		select string_agg(currency || amount, E'\n') from (
			select sum(amount) as amount, currency
			from entries
			where account[1] = '负债'
			and date < now() + interval '1 month'
			group by currency
		) t0
		where amount <> 0
	)::text
	|| E'\n-----\n'
	|| (
		select string_agg(
			kind || ' ' || currency || amount, E'\n'
			order by amount asc
		) 
		from (
			select sum(amount) as amount, currency, account[2] as kind
			from entries
			where account[1] = '负债'
			and date < now() + interval '1 month'
			group by currency, account[2]
		) t0
		where amount <> 0
	)
	AS 一月负债

	`,

	// net_asset_changes
	`
	create view net_asset_changes as
	select 
	to_char(d, 'YYYY-MM') AS 月份,
	d - lag(d, 1) over (partition by c order by d asc) as 日数,
	net AS 净资产,
	c,
	net - lag(net, 1) over (partition by c order by d asc) as 变动
	from (
		select 
		c,
		d,
		COALESCE((
			select sum(amount)
			from entries
			where account[1] = '资产'
			and currency = c
			and date < d
		), 0) 
		+ COALESCE((
			select sum(amount)
			from entries
			where account[1] = '负债'
			and currency = c
			and date < d
		), 0) net
		from (
			select distinct date_trunc('month', date) as d , currency as c from entries
			where currency in ('￥', '$')
		) t0
		where d < now()
	) t1
	order by d desc
	`,

	// assurance
	`
	create view assurance as
	select 
	year, d,  sum(amount)
	from (
		select
		*,
		extract(year from date) as year,
		(
			select account[3] || '：' || account[4] from entries b
			where transaction = entries.transaction
			and account[1] = '保险' and account[2] = '生效'
		) as d
		from entries
		where account[1] = '支出' and account[2] = '保险'
	) t0
	group by d, year
	order by year asc, sum(amount) desc
	`,

	//
}

func intervalStat(groupBy string) string {
	return `
	select 
	to_char(` + groupBy + `, 'YYYY-MM-DD') as span,

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

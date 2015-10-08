package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/width"
)

func init() {
	flag.Parse()
}

var (
	pt   = fmt.Printf
	sp   = fmt.Sprintf
	fp   = fmt.Fprintf
	zero = new(big.Rat)
)

func main() {
	args := flag.Args()
	if len(args) < 1 {
		pt("usage: %s [file]\n", os.Args[0])
		return
	}

	type Currency string
	type Entry struct {
		Account  string
		Currency Currency
		Amount   *big.Rat
	}
	type Transaction struct {
		When    time.Time
		What    string
		Entries []Entry
	}

	// parse
	location, err := time.LoadLocation("Asia/Hong_Kong")
	ce(err, "load location")
	transactions := []Transaction{}
	bs, err := ioutil.ReadFile(args[0])
	ce(err, "read file")
	entries := strings.Split(string(bs), "\n\n")
	for _, bs := range entries {
		bs = strings.TrimSpace(bs)
		lines := Strs(strings.Split(bs, "\n"))
		lines = lines.Filter(func(s string) bool {
			return !strings.HasPrefix(s, "#")
		})
		if len(lines) == 0 {
			continue
		}
		when, what := spaceSplit(strings.TrimSpace(lines[0]))
		if len(when) == 0 {
			continue
		}
		parts := strings.Split(when, "-")
		year, err := strconv.Atoi(parts[0])
		ce(err, "parse year")
		if year < 100 {
			year += 2000
		}
		month, err := strconv.Atoi(parts[1])
		ce(err, "parse month")
		day, err := strconv.Atoi(parts[2])
		ce(err, "parse day")
		transaction := Transaction{
			When: time.Date(year, time.Month(month), day, 0, 0, 0, 0, location),
			What: what,
		}
		for _, line := range lines[1:] {
			line = strings.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			account, res := spaceSplit(line)
			entry := Entry{
				Account: account,
			}
			if len(res) == 0 {
				log.Fatalf("invalid entry %s", line)
			}
			runes := []rune(res)
			currency := Currency(runes[0])
			entry.Currency = currency
			amount := new(big.Rat)
			_, err := fmt.Sscan(string(runes[1:]), amount)
			ce(err, sp("parse amount %v", string(runes[1:])))
			entry.Amount = amount
			transaction.Entries = append(transaction.Entries, entry)
		}
		transactions = append(transactions, transaction)
	}

	// calculate balance
	zero := new(big.Rat)
	for _, transaction := range transactions {
		balance := make(map[Currency]*big.Rat)
		for _, entry := range transaction.Entries {
			if _, ok := balance[entry.Currency]; !ok {
				balance[entry.Currency] = new(big.Rat)
			}
			balance[entry.Currency].Add(balance[entry.Currency], entry.Amount)
		}
		for _, n := range balance {
			if n.Cmp(zero) != 0 {
				log.Fatalf("not balance: %s", transaction.What)
			}
		}
	}

	type Amount struct {
		Debit  *big.Rat
		Credit *big.Rat
	}
	type Account map[Currency]*Amount

	// account summaries
	accounts := make(map[string]Account)
	sum := func(name string, currency Currency, n *big.Rat) {
		var account Account
		var ok bool
		if account, ok = accounts[name]; !ok {
			account = make(Account)
			accounts[name] = account
		}
		var amount *Amount
		if amount, ok = account[currency]; !ok {
			amount = &Amount{
				Debit:  new(big.Rat),
				Credit: new(big.Rat),
			}
		}
		if n.Sign() == -1 {
			amount.Credit.Add(amount.Credit, n)
		} else {
			amount.Debit.Add(amount.Debit, n)
		}
		account[currency] = amount
	}
	for _, transaction := range transactions {
		for _, entry := range transaction.Entries {
			var name []rune
			for _, r := range entry.Account {
				if r == '：' {
					sum(string(name), entry.Currency, entry.Amount)
				}
				name = append(name, r)
			}
			sum(string(name), entry.Currency, entry.Amount)
		}
	}
	names := []string{}
	maxNameLen := 0
	for name := range accounts {
		names = append(names, name)
		nameLen := 0
		level := 0
		for _, r := range name {
			if r == '：' {
				level++
				nameLen = level * 2
			} else if width.LookupRune(r).Kind() == width.EastAsianWide {
				nameLen += 2
			} else {
				nameLen += 1
			}
			if nameLen > maxNameLen {
				maxNameLen = nameLen
			}
		}
	}
	sort.Strings(names)
	for _, name := range names {
		account := accounts[name]
		n := []rune{}
		level := 0
		for _, r := range name {
			if r == '：' {
				n = n[0:0]
				level++
			} else {
				n = append(n, r)
			}
		}
		buf := new(bytes.Buffer)
		buf.WriteString(pad(strings.Repeat("  ", level)+string(n), maxNameLen))
		nonZero := false
		for currency, amount := range account {
			balance := new(big.Rat)
			balance.Set(amount.Credit)
			balance.Add(balance, amount.Debit)
			if balance.Sign() != 0 {
				nonZero = true
				credit := new(big.Rat)
				credit.Set(amount.Credit)
				credit.Abs(credit)
				fp(buf, " %s%s %s - %s", currency,
					pad(balance.FloatString(2), 10),
					pad(amount.Debit.FloatString(2), 10),
					pad(credit.FloatString(2), 10),
				)
			}
		}
		fp(buf, "\n")
		if nonZero {
			pt("%s", buf.Bytes())
		}
	}

}

var spaceSplitPattern = regexp.MustCompile(`\s+`)

func spaceSplit(s string) (string, string) {
	ss := spaceSplitPattern.Split(s, 2)
	if len(ss) == 1 {
		return ss[0], ""
	}
	return ss[0], ss[1]
}

func pad(s string, l int) string {
	n := 0
	for _, r := range s {
		if width.LookupRune(r).Kind() == width.EastAsianWide {
			n += 2
		} else {
			n += 1
		}
	}
	if res := l - n; res > 0 {
		return s + strings.Repeat(" ", res)
	}
	return s
}

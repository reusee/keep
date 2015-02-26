package main

import (
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
)

func init() {
	flag.Parse()
}

var (
	pt = fmt.Printf
	sp = fmt.Sprintf
)

func main() {
	args := flag.Args()
	if len(args) < 1 {
		pt("usage: %s [file]\n", os.Args[0])
		return
	}

	checkErr := func(msg string, err error) {
		if err != nil {
			log.Fatalf("%s: %v", msg, err)
		}
	}

	type Entry struct {
		Account  string
		Currency string
		Amount   *big.Rat
	}
	type Transaction struct {
		When    time.Time
		What    string
		Entries []Entry
	}

	// parse
	location, err := time.LoadLocation("Asia/Hong_Kong")
	checkErr("load location", err)
	transactions := []Transaction{}
	bs, err := ioutil.ReadFile(args[0])
	checkErr("read file", err)
	entries := strings.Split(string(bs), "\n\n")
	for _, bs := range entries {
		lines := strings.Split(bs, "\n")
		if len(lines) == 0 {
			continue
		}
		when, what := spaceSplit(strings.TrimSpace(lines[0]))
		parts := strings.Split(when, "-")
		year, err := strconv.Atoi(parts[0])
		checkErr("parse year", err)
		if year < 100 {
			year += 2000
		}
		month, err := strconv.Atoi(parts[1])
		checkErr("parse month", err)
		day, err := strconv.Atoi(parts[2])
		checkErr("parse day", err)
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
			currency := string(runes[0])
			entry.Currency = currency
			amount := new(big.Rat)
			_, err := fmt.Sscan(string(runes[1:]), amount)
			checkErr(sp("parse amount %v", string(runes[1:])), err)
			entry.Amount = amount
			transaction.Entries = append(transaction.Entries, entry)
		}
		transactions = append(transactions, transaction)
	}

	// calculate balance
	zero := new(big.Rat)
	for _, transaction := range transactions {
		balance := make(map[string]*big.Rat)
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

	// account summaries
	accounts := make(map[string]map[string]*big.Rat)
	for _, transaction := range transactions {
		for _, entry := range transaction.Entries {
			var account map[string]*big.Rat
			var ok bool
			if account, ok = accounts[entry.Account]; !ok {
				account = make(map[string]*big.Rat)
				accounts[entry.Account] = account
			}
			var amount *big.Rat
			if amount, ok = account[entry.Currency]; !ok {
				amount = new(big.Rat)
			}
			amount.Add(amount, entry.Amount)
			account[entry.Currency] = amount
		}
	}
	names := []string{}
	for name := range accounts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		account := accounts[name]
		pt("%s", name)
		for currency, amount := range account {
			pt(" %s%s", currency, amount.FloatString(2))
		}
		pt("\n")
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

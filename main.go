package main

import (
	"flag"
	"io/ioutil"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func main() {
	var fromStr string
	flag.StringVar(&fromStr, "from", "0-1-1", "from date")
	var toStr string
	flag.StringVar(&toStr, "to", "9999-1-1", "to date")
	flag.Parse()

	// options
	parseDate := func(str string) time.Time {
		parts := strings.Split(str, "-")
		if len(parts) != 3 {
			panic(me(nil, "bad date: %s", str))
		}
		year, err := strconv.Atoi(parts[0])
		ce(err, "parse year: %s", str)
		month, err := strconv.Atoi(parts[1])
		ce(err, "parse month: %s", str)
		day, err := strconv.Atoi(parts[2])
		ce(err, "parse day: %s", str)
		return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.Local)
	}
	fromTime := parseDate(fromStr).Add(-time.Hour)
	toTime := parseDate(toStr).Add(time.Hour)

	// usage
	args := flag.Args()
	if len(args) < 1 {
		pt("usage: %s [options] <file path>\n", os.Args[0])
		flag.Usage()
		return
	}

	// read ledger file
	ledgerPath := args[0]
	contentBytes, err := ioutil.ReadFile(ledgerPath)
	ce(err, "read ledger")
	content := string(contentBytes)
	content = strings.Replace(content, "\r\n", "\n", -1)
	content = strings.Replace(content, "\r", "\n", -1)

	// parse blocks
	var blocks [][]string
	var block []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 0 {
			if len(block) > 0 {
				blocks = append(blocks, block)
				block = []string{}
			}
		} else {
			block = append(block, line)
		}
	}
	if len(block) > 0 {
		blocks = append(blocks, block)
	}

	// transaction
	type Account struct {
		Name     string
		Subs     map[string]*Account
		Parent   *Account
		Balances map[string]*big.Rat
	}
	type Entry struct {
		Account     *Account
		Currency    string
		Amount      *big.Rat
		Description string
	}
	type Transaction struct {
		Year        int
		Month       int
		Day         int
		Description string
		Entries     []*Entry
	}
	var transactions []*Transaction

	rootAccount := &Account{
		Name:     "root",
		Subs:     make(map[string]*Account),
		Parent:   nil,
		Balances: make(map[string]*big.Rat),
	}
	var getAccount func(root *Account, path []string) *Account
	getAccount = func(root *Account, path []string) *Account {
		if len(path) == 0 {
			panic(me(nil, "bad account: %v", path))
		} else if len(path) == 1 {
			name := path[0]
			account, ok := root.Subs[name]
			if ok {
				return account
			}
			account = &Account{
				Name:     name,
				Subs:     make(map[string]*Account),
				Parent:   root,
				Balances: make(map[string]*big.Rat),
			}
			root.Subs[name] = account
			return account
		}
		name := path[0]
		account, ok := root.Subs[name]
		if !ok {
			account = &Account{
				Name:     name,
				Subs:     make(map[string]*Account),
				Parent:   root,
				Balances: make(map[string]*big.Rat),
			}
			root.Subs[name] = account
		}
		return getAccount(account, path[1:])
	}

	// collect transactions
	for _, block := range blocks {
		n := 0
		transaction := new(Transaction)

		// parse
		for _, line := range block {
			if strings.HasPrefix(line, "#") {
				continue
			}
			n++

			if n == 1 {
				// transaction header
				parts := blanksPattern.Split(line, 2)
				if len(parts) != 2 {
					panic(me(nil, "bad transaction header: %s", line))
				}

				dateStr := parts[0]
				dateParts := strings.Split(dateStr, "-")
				if len(dateParts) != 3 {
					panic(me(nil, "bad date: %s", line))
				}
				year, err := strconv.Atoi(dateParts[0])
				ce(err, "parse year: %s", line)
				transaction.Year = year
				month, err := strconv.Atoi(dateParts[1])
				ce(err, "parse month: %s", line)
				transaction.Month = month
				day, err := strconv.Atoi(dateParts[2])
				ce(err, "parse day: %s", line)
				transaction.Day = day

				transaction.Description = parts[1]

			} else {
				// entry
				parts := blanksPattern.Split(line, 3)
				if len(parts) < 2 {
					panic(me(nil, "bad entry: %s", line))
				}
				entry := new(Entry)

				accountStr := parts[0]
				account := getAccount(rootAccount, strings.Split(accountStr, "："))
				entry.Account = account

				currency, runeSize := utf8.DecodeRuneInString(parts[1])
				entry.Currency = string(currency)
				amountStr := parts[1][runeSize:]
				amount := new(big.Rat)
				_, ok := amount.SetString(amountStr)
				if !ok {
					panic(me(nil, "bad amount: %s", amountStr))
				}
				entry.Amount = amount

				if len(parts) > 2 {
					entry.Description = parts[2]
				}

				transaction.Entries = append(transaction.Entries, entry)
			}

		}

		if transaction.Year == 0 {
			// empty
			continue
		}
		t := time.Date(transaction.Year, time.Month(transaction.Month), transaction.Day,
			0, 0, 0, 0, time.Local)
			if t.Before(fromTime) || t.After(toTime) {
				// out of range
				continue
			}

		// check balance
		sum := big.NewRat(0, 1)
		for _, entry := range transaction.Entries {
			sum.Add(sum, entry.Amount)
			// update account balance
			account := entry.Account
			for account != nil {
				balance, ok := account.Balances[entry.Currency]
				if !ok {
					balance = big.NewRat(0, 1)
					account.Balances[entry.Currency] = balance
				}
				balance.Add(balance, entry.Amount)
				account = account.Parent
			}
		}
		if !(sum.Cmp(zeroRat) == 0) {
			panic(me(nil, "not balanced: %s", strings.Join(block, "\n")))
		}

		transactions = append(transactions, transaction)
	}

	// print accounts
	var printAccount func(account *Account, level int)
	printAccount = func(account *Account, level int) {
		allZero := true
		for _, balance := range account.Balances {
			if balance.Cmp(zeroRat) != 0 {
				allZero = false
				break
			}
		}
		if allZero && account != rootAccount {
			return
		}
		pt("%s%s", strings.Repeat(" │    ", level), account.Name)
		var currencyNames []string
		for name := range account.Balances {
			currencyNames = append(currencyNames, name)
		}
		sort.Strings(currencyNames)
		for _, name := range currencyNames {
			balance := account.Balances[name]
			pt(" %s%s", name, balance.FloatString(2))
		}
		pt("\n")

		var subNames []string
		for name := range account.Subs {
			subNames = append(subNames, name)
		}
		sort.Strings(subNames)
		for _, name := range subNames {
			printAccount(account.Subs[name], level+1)
		}
	}
	var subNames []string
	for name := range rootAccount.Subs {
		subNames = append(subNames, name)
	}
	sort.Strings(subNames)
	for _, name := range subNames {
		printAccount(rootAccount.Subs[name], 0)
	}

}

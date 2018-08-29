package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"math/big"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/width"
)

var (
	accountSeparatePattern = regexp.MustCompile(`：|:`)
	sharePricePattern      = regexp.MustCompile(`[0-9]+\.[0-9]{3}`)
	monthPattern           = regexp.MustCompile(`^[0-9x]{4}$`)
)

func main() {
	var fromStr string
	flag.StringVar(&fromStr, "from", "1000-01-01", "from date")
	var toStr string
	flag.StringVar(&toStr, "to", "9999-01-01", "to date")
	var cmdProperties bool
	flag.BoolVar(&cmdProperties, "props", false, "show properties")
	var cmdMonthlyExpenses bool
	flag.BoolVar(&cmdMonthlyExpenses, "monthly", false, "show monthly expenses")
	var flagToday bool
	flag.BoolVar(&flagToday, "today", false, "set from and to as today")
	var noAmount bool
	flag.BoolVar(&noAmount, "no-amount", false, "do not display amount")

	flag.Parse()

	// options
	parseDate := func(str string) time.Time {
		str = strings.Replace(str, "/", "-", -1)
		str = strings.Replace(str, ".", "-", -1)
		t, err := time.Parse("2006-01-02", str)
		ce(err, "bad date: %s", str)
		return t
	}
	fromTime := parseDate(fromStr).Add(-time.Hour)
	toTime := parseDate(toStr).Add(time.Hour)
	if flagToday {
		now := time.Now()
		fromTime = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		toTime = time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.Local)
	}

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
		Name        string
		Subs        map[string]*Account
		Parent      *Account
		Balances    map[string]*big.Rat
		Proportions map[string]*big.Rat
	}
	type Entry struct {
		Time  time.Time
		Year  int
		Month int
		Day   int

		Account     *Account
		Currency    string
		Amount      *big.Rat
		Description string
	}
	type Transaction struct {
		Description string
		Entries     []*Entry
		TimeFrom    time.Time
		TimeTo      time.Time
	}
	var transactions []*Transaction

	rootAccount := &Account{
		Name:        "root",
		Subs:        make(map[string]*Account),
		Parent:      nil,
		Balances:    make(map[string]*big.Rat),
		Proportions: make(map[string]*big.Rat),
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
				Name:        name,
				Subs:        make(map[string]*Account),
				Parent:      root,
				Balances:    make(map[string]*big.Rat),
				Proportions: make(map[string]*big.Rat),
			}
			root.Subs[name] = account
			return account
		}
		name := path[0]
		account, ok := root.Subs[name]
		if !ok {
			account = &Account{
				Name:        name,
				Subs:        make(map[string]*Account),
				Parent:      root,
				Balances:    make(map[string]*big.Rat),
				Proportions: make(map[string]*big.Rat),
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
		var t time.Time
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
				t = parseDate(parts[0])
				if transaction.TimeFrom.IsZero() || t.Before(transaction.TimeFrom) {
					transaction.TimeFrom = t
				}
				if transaction.TimeTo.IsZero() || t.After(transaction.TimeTo) {
					transaction.TimeTo = t
				}
				transaction.Description = parts[1]

			} else {
				// entry
				parts := blanksPattern.Split(line, 3)
				if len(parts) < 2 {
					panic(me(nil, "bad entry: %s", line))
				}
				entry := new(Entry)

				accountStr := parts[0]
				account := getAccount(rootAccount, accountSeparatePattern.Split(accountStr, -1))
				entry.Account = account

				currency, runeSize := utf8.DecodeRuneInString(parts[1])
				entry.Currency = string(currency)
				amountStr := parts[1][runeSize:]
				amount, err := parseAmount(amountStr)
				if err != nil {
					panic(me(err, "bad amount: %s", amountStr))
				}
				entry.Amount = amount

				if len(parts) > 2 {
					entry.Description = parts[2]
				}

				if inlineDate := inlineDatePattern.FindString(entry.Description); inlineDate != "" {
					inlineT := parseDate(inlineDate[1:])
					entry.Time = inlineT
					entry.Year = inlineT.Year()
					entry.Month = int(inlineT.Month())
					entry.Day = inlineT.Day()
				} else {
					entry.Time = t
					entry.Year = t.Year()
					entry.Month = int(t.Month())
					entry.Day = t.Day()
				}

				transaction.Entries = append(transaction.Entries, entry)
			}

		}

		if t.IsZero() {
			continue
		}
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
				if sharePricePattern.MatchString(account.Name) {
					if !flagToday {
						isNegative := strings.HasPrefix(account.Parent.Name, "-") ||
							strings.HasPrefix(account.Name, "-")
						if !isNegative {
							if balance.Sign() < 0 {
								panic(me(nil, "negative balance in stock share account"))
							}
						} else {
							if balance.Sign() > 0 {
								panic(me(nil, "positive balance in stock share account"))
							}
						}
					}
				}
				account = account.Parent
			}
		}
		if !(sum.Cmp(zeroRat) == 0) {
			panic(me(nil, "not balanced: %s", strings.Join(block, "\n")))
		}

		transactions = append(transactions, transaction)
	}

	// calculate proportions
	var calculateProportion func(*Account)
	calculateProportion = func(account *Account) {
		for _, sub := range account.Subs {
			for currency, balance := range sub.Balances {
				if account.Balances[currency].Sign() != 0 {
					b := big.NewRat(0, 1)
					b.Set(balance)
					sub.Proportions[currency] = b.Quo(balance, account.Balances[currency])
					b.Abs(b)
				}
				calculateProportion(sub)
			}
		}
	}
	calculateProportion(rootAccount)

	// print accounts
	var printAccount func(account *Account, level int, nameLen int)
	printAccount = func(account *Account, level int, nameLen int) {
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
		pt(
			"%s%s",
			strings.Repeat(" │    ", level),
			padToLen(account.Name, nameLen),
		)
		isStockShareAccount := sharePricePattern.MatchString(account.Name)
		var currencyNames []string
		for name := range account.Balances {
			currencyNames = append(currencyNames, name)
		}
		sort.Strings(currencyNames)
		for _, name := range currencyNames {
			balance := account.Balances[name]
			var proportion string
			if p, ok := account.Proportions[name]; ok {
				proportion = " " + p.Mul(p, big.NewRat(100, 1)).FloatString(3) + "%"
			}
			if !noAmount {
				pt(" %s", name)
			}
			if isStockShareAccount {
				price := new(big.Rat)
				price, _ = price.SetString(account.Name)
				pt("%s*", price.FloatString(3))
				nShare := new(big.Rat)
				nShare.Set(balance)
				nShare.Quo(nShare, price)
				pt("%s", nShare.FloatString(1))
			} else {
				if !noAmount {
					pt("%s", balance.FloatString(2))
				}
				pt("%s", proportion)
			}
		}
		pt("\n")

		var subNames []string
		for name := range account.Subs {
			subNames = append(subNames, name)
		}

		allIsLeaf := func() bool {
			for _, sub := range account.Subs {
				sum := big.NewRat(0, 1)
				for _, balance := range sub.Balances {
					sum.Add(sum, balance)
				}
				if sum.Sign() == 0 {
					continue
				}
				if len(sub.Subs) > 0 {
					return false
				}
			}
			return true
		}()
		allIsMonths := func() bool {
			if !allIsLeaf {
				return false
			}
			for _, name := range subNames {
				sub := account.Subs[name]
				sum := big.NewRat(0, 1)
				for _, balance := range sub.Balances {
					sum.Add(sum, balance)
				}
				if sum.Sign() == 0 {
					continue
				}
				if !monthPattern.MatchString(name) {
					return false
				}
			}
			return true
		}()
		allIsSharePrices := func() bool {
			if !allIsLeaf {
				return false
			}
			for _, name := range subNames {
				sub := account.Subs[name]
				sum := big.NewRat(0, 1)
				for _, balance := range sub.Balances {
					sum.Add(sum, balance)
				}
				if sum.Sign() == 0 {
					continue
				}
				if !sharePricePattern.MatchString(name) {
					return false
				}
			}
			return true
		}()

		sort.Slice(subNames, func(i, j int) bool {
			a := account.Subs[subNames[i]]
			b := account.Subs[subNames[j]]
			if allIsMonths {
				// 月份排序
				return subNames[i] < subNames[j]
			}
			if allIsSharePrices {
				// 价格排序
				priceA := new(big.Rat)
				priceA, ok := priceA.SetString(subNames[i])
				if !ok {
					panic(fmt.Sprintf("bad price: %s", subNames[i]))
				}
				priceB := new(big.Rat)
				priceB, ok = priceB.SetString(subNames[j])
				if !ok {
					panic(fmt.Sprintf("bad price: %s", subNames[j]))
				}
				return priceA.Cmp(priceB) > 0
			}
			if level == 0 {
				return subNames[i] < subNames[j]
			}
			// 金额排序
			sumA := big.NewRat(0, 1)
			for _, balance := range a.Balances {
				sumA.Add(sumA, balance)
			}
			sumB := big.NewRat(0, 1)
			for _, balance := range b.Balances {
				sumB.Add(sumB, balance)
			}
			return sumA.Cmp(sumB) > 0
		})
		subNameLen := 0
		for _, name := range subNames {
			l := displayWidth(name)
			if l > subNameLen {
				subNameLen = l
			}
		}
		for _, name := range subNames {
			printAccount(account.Subs[name], level+1, subNameLen)
		}
	}
	printAccount(rootAccount, 0, 0)

	if cmdProperties {
		accountNames := map[string]bool{
			"书籍":   true,
			"保健品":  true,
			"性用品":  true,
			"消耗品":  true,
			"物品":   true,
			"电子":   true,
			"数码":   true,
			"药物":   true,
			"衣物服饰": true,
		}
		accounts := map[*Account]bool{}
		for name, account := range rootAccount.Subs["支出"].Subs {
			if accountNames[name] {
				accounts[account] = true
			}
		}
		var ts []*Transaction
	loop_t:
		for _, t := range transactions {
			for _, entry := range t.Entries {
				if accounts[entry.Account] {
					ts = append(ts, t)
					continue loop_t
				}
			}
		}
		sort.Slice(ts, func(i, j int) bool {
			return ts[i].TimeFrom.Before(ts[j].TimeFrom)
		})
		for _, t := range ts {
			pt("%s %s\n", t.TimeFrom.Format("01-02"), t.Description)
		}
	}

	if cmdMonthlyExpenses {
		accounts := make(map[*Account]bool)
		for _, account := range rootAccount.Subs["支出"].Subs {
			accounts[account] = true
		}
		monthEntries := make(map[string][]*Entry)
		for _, transaction := range transactions {
			for _, entry := range transaction.Entries {
				if accounts[entry.Account] {
					// is expense
					monthStr := fmt.Sprintf("%04d-%02d", entry.Year, entry.Month)
					monthEntries[monthStr] = append(monthEntries[monthStr], entry)
				}
			}
		}
		var months []string
		for month := range monthEntries {
			months = append(months, month)
		}
		sort.Strings(months)
		for _, month := range months {
			entries := monthEntries[month]
			sums := make(map[string]*big.Rat)
			for _, entry := range entries {
				if sums[entry.Currency] == nil {
					sums[entry.Currency] = big.NewRat(0, 1)
				}
				sums[entry.Currency].Add(sums[entry.Currency], entry.Amount)
			}
			pt("%s", month)
			for cur, sum := range sums {
				pt(" %s%s", cur, sum.FloatString(2))
			}
			pt("\n")
		}
	}

}

func parseAmount(str string) (*big.Rat, error) {
	expr, err := parser.ParseExpr(str)
	if err != nil {
		return nil, err
	}
	value, err := evalExpr(expr)
	if err != nil {
		return nil, err
	}
	return value, nil
}

func evalExpr(expr ast.Expr) (*big.Rat, error) {
	switch expr := expr.(type) {

	case *ast.BasicLit:
		r := new(big.Rat)
		a, ok := r.SetString(expr.Value)
		if !ok {
			return nil, me(nil, "bad expression: %s", expr.Value)
		}
		return a, nil

	case *ast.UnaryExpr:
		switch expr.Op {
		case token.SUB:
			v, err := evalExpr(expr.X)
			if err != nil {
				return nil, err
			}
			return v.Neg(v), nil
		}

	case *ast.BinaryExpr:
		x, err := evalExpr(expr.X)
		if err != nil {
			return nil, err
		}
		y, err := evalExpr(expr.Y)
		if err != nil {
			return nil, err
		}
		switch expr.Op {
		case token.MUL:
			return x.Mul(x, y), nil
		case token.SUB:
			return x.Sub(x, y), nil
		case token.ADD:
			return x.Add(x, y), nil
		}

	case *ast.ParenExpr:
		return evalExpr(expr.X)

	}

	pt("%#v\n", expr)
	return nil, me(nil, "unkndown expr")

}

func displayWidth(s string) int {
	l := 0
	for _, r := range s {
		properties := width.LookupRune(r)
		switch properties.Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			l += 2
		default:
			l += 1
		}
	}
	return l
}

func padToLen(s string, l int) string {
	b := new(strings.Builder)
	b.WriteString(s)
	l -= displayWidth(s)
	for ; l > 0; l-- {
		b.WriteByte(' ')
	}
	return b.String()
}

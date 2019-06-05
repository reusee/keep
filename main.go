package main

import (
	"bytes"
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
	blanksPattern          = regexp.MustCompile(`\s+`)
	inlineDatePattern      = regexp.MustCompile(`@[0-9]{4}[/.-][0-9]{2}[/.-][0-9]{2}`)
	commentLinePattern     = regexp.MustCompile(`^\s*(#|//)`)
	yearMonthPattern       = regexp.MustCompile(`[0-9]{4}`)
	entryTagPattern        = regexp.MustCompile(`<[^>]+>`)
)

type Account struct {
	Name        string
	Subs        map[string]*Account
	Parent      *Account
	Balances    map[string]*big.Rat
	Proportions map[string]*big.Rat
	TimeFrom    time.Time
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
	Tags        map[string]bool
}

type Transaction struct {
	Description string
	Entries     []*Entry
	TimeFrom    time.Time
	TimeTo      time.Time
}

func main() {
	var noAmount bool
	flag.BoolVar(&noAmount, "no-amount", false, "do not display amount")
	var cmdSQL bool
	flag.BoolVar(&cmdSQL, "sql", false, "load and show SQL ui")

	flag.Parse()

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

	type Block struct {
		Line       int
		HeaderDate string
		Contents   []string
	}

	// parse blocks
	var blocks []Block
	var contents []string
	i := 0
	for _, line := range strings.Split(content, "\n") {
		i++
		line = strings.TrimSpace(line)
		if len(line) == 0 {
			if len(contents) > 0 {
				date := strings.Split(line, " ")[0]
				blocks = append(blocks, Block{
					Line:       i - len(contents),
					HeaderDate: date,
					Contents:   contents,
				})
				contents = []string{}
			}
		} else {
			contents = append(contents, line)
		}
	}
	if len(contents) > 0 {
		blocks = append(blocks, Block{
			Line:     i - len(contents),
			Contents: contents,
		})
	}
	sort.SliceStable(blocks, func(i, j int) bool {
		block1 := blocks[i]
		block2 := blocks[j]
		if block1.HeaderDate != block2.HeaderDate {
			return block1.HeaderDate < block2.HeaderDate
		}
		return block1.Line < block2.Line
	})

	formatDone := make(chan bool)
	go func() {
		// format
		out := new(bytes.Buffer)
		write := func(s string) {
			if _, err := out.WriteString(s); err != nil {
				panic(err)
			}
		}
		for _, block := range blocks {
			write(block.Contents[0] + "\n")
			var lineParts [][]string
			var widths [3]int
			for _, line := range block.Contents[1:] {
				parts := blanksPattern.Split(line, 3)
				lineParts = append(lineParts, parts)
				for i, part := range parts {
					width := displayWidth(part)
					if width > widths[i] {
						widths[i] = width
					}
				}
			}
			for _, parts := range lineParts {
				for i, part := range parts {
					if i > 0 && len(part) > 0 {
						write("    ")
					}
					if i == len(parts)-1 {
						part = strings.TrimRight(part, " ")
					} else {
						part = padToLen(part, widths[i])
					}
					write(part)
				}
				write("\n")
			}
			write("\n")
		}
		formatted := false
		if !bytes.Equal(contentBytes, out.Bytes()) {
			ce(ioutil.WriteFile(ledgerPath+".tmp", out.Bytes(), 0644))
			ce(os.Rename(ledgerPath+".tmp", ledgerPath))
			formatted = true
		}
		formatDone <- formatted
	}()

	// transaction
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
	var lastT time.Time
	for _, block := range blocks {
		n := 0
		transaction := new(Transaction)

		reportError := func(format string, args ...interface{}) {
			pt(
				"%s at line %d:\n%s",
				fmt.Sprintf(format, args...),
				block.Line,
				strings.Join(block.Contents, "\n"),
			)
			panic("bad block")
		}

		// parse
		var t time.Time
		for _, line := range block.Contents {
			n++

			if n == 1 {
				// transaction header
				parts := blanksPattern.Split(line, 2)
				if len(parts) != 2 {
					reportError("bad header")
				}
				t = parseDate(parts[0])
				if transaction.TimeFrom.IsZero() || t.Before(transaction.TimeFrom) {
					transaction.TimeFrom = t
				}
				if transaction.TimeTo.IsZero() || t.After(transaction.TimeTo) {
					transaction.TimeTo = t
				}
				transaction.Description = parts[1]

				if !lastT.IsZero() && t.Before(lastT) {
					reportError("bad time")
				}
				lastT = t

			} else {
				if commentLinePattern.MatchString(line) {
					continue
				}

				// entry
				parts := blanksPattern.Split(line, 3)
				if len(parts) < 2 {
					reportError("bad entry")
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
					reportError("bad amount")
				}
				entry.Amount = amount

				if len(parts) > 2 {
					entry.Description = parts[2]
				}

				entry.Tags = make(map[string]bool)
				for _, tag := range entryTagPattern.FindAllString(entry.Description, -1) {
					entry.Tags[tag] = true
				}

				var entryTime time.Time
				if inlineDate := inlineDatePattern.FindString(entry.Description); inlineDate != "" {
					entryTime = parseDate(inlineDate[1:])
				} else if yearMonthPattern.MatchString(account.Name) && account.Top().Name == "负债" {
					entryTime, err = time.Parse("0601", account.Name)
					ce(err)
				} else {
					entryTime = t
				}
				entry.Time = entryTime
				entry.Year = entryTime.Year()
				entry.Month = int(entryTime.Month())
				entry.Day = entryTime.Day()

				if account.TimeFrom.IsZero() || entryTime.Before(account.TimeFrom) {
					account.TimeFrom = entryTime
				}

				transaction.Entries = append(transaction.Entries, entry)
			}

		}

		// virtual entries
		for _, entry := range transaction.Entries {
			if !(itemKinds[entry.Account.Name] && entry.Account.Parent.Name == "支出") {
				continue
			}
			if entry.Tags["<!item>"] {
				continue
			}

			e := new(Entry)
			e.Account = getAccount(rootAccount, []string{
				"物品", "可用", entry.Account.Name,
				entry.Time.Format("2006-01-02 ") +
					transaction.Description,
			})
			e.Currency = "/"
			e.Amount = big.NewRat(int64(entry.Amount.Sign()), 1)
			e.Time = entry.Time
			e.Year = entry.Year
			e.Month = entry.Month
			e.Day = entry.Day
			transaction.Entries = append(transaction.Entries, e)
			e = new(Entry)
			e.Account = getAccount(rootAccount, []string{
				"物品", "购买", entry.Account.Name,
			})
			e.Currency = "/"
			e.Amount = big.NewRat(int64(-entry.Amount.Sign()), 1)
			e.Time = entry.Time
			e.Year = entry.Year
			e.Month = entry.Month
			e.Day = entry.Day
			transaction.Entries = append(transaction.Entries, e)
		}

		if t.IsZero() {
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
					isNegative := strings.HasPrefix(account.Parent.Name, "-") ||
						strings.HasPrefix(account.Name, "-")
					if !isNegative {
						if balance.Sign() < 0 {
							reportError("negative balance in stock share account")
						}
					} else {
						if balance.Sign() > 0 {
							reportError("positive balance in stock share account")
						}
					}
				}
				account = account.Parent
			}
		}
		if !(sum.Cmp(zeroRat) == 0) {
			reportError("not balanced")
		}

		transactions = append(transactions, transaction)
	}

	if cmdSQL {
		sqlInterface(rootAccount, transactions)
		return
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
	var printAccount func(account *Account, level int, nameLen int, noSkipZeroAmount bool)
	printAccount = func(account *Account, level int, nameLen int, noSkipZeroAmount bool) {
		if account.Parent == rootAccount && noSkipZeroAmountAccounts[account.Name] {
			noSkipZeroAmount = true
		}
		allZero := true
		for _, balance := range account.Balances {
			if balance.Cmp(zeroRat) != 0 {
				allZero = false
				break
			}
		}
		if allZero && account != rootAccount && !noSkipZeroAmount {
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
					if name == "/" {
						pt("%s", balance.FloatString(0))
					} else {
						pt("%s", balance.FloatString(2))
					}
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

		sort.SliceStable(subNames, func(i, j int) bool {
			a := account.Subs[subNames[i]]
			b := account.Subs[subNames[j]]
			weightA, ok1 := sortWeight[sortWeightKey{level + 1, a.Name}]
			weightB, ok2 := sortWeight[sortWeightKey{level + 1, b.Name}]
			if ok1 && !ok2 {
				return false
			} else if !ok1 && ok2 {
				return true
			} else if ok1 && ok2 {
				return weightA < weightB
			}
			// '/' 为单位的
			if len(account.Balances) == 1 && func() bool {
				for currency := range account.Balances {
					if currency == "/" {
						return true
					}
				}
				return false
			}() {
				return a.Name < b.Name
			}
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
		skip := false
		for _, name := range subNames {
			subAccount := account.Subs[name]
			if subAccount.TimeFrom.Sub(time.Now()) > time.Hour*24*365 {
				skip = true
				continue
			}
			printAccount(subAccount, level+1, subNameLen, func() bool {
				if len(subAccount.Subs) == 0 {
					return false
				}
				return noSkipZeroAmount
			}())
		}
		if skip {
			pt(
				"%s[...]\n",
				strings.Repeat(" │    ", level+1),
			)
		}
	}
	printAccount(rootAccount, 0, 0, false)

	formatted := <-formatDone
	if formatted {
		pt("formatted\n")
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

func evalExpr(expr ast.Expr) (result *big.Rat, err error) {
	defer he(&err)

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
			ce(err)
			return v.Neg(v), nil
		}

	case *ast.BinaryExpr:
		x, err := evalExpr(expr.X)
		ce(err)
		y, err := evalExpr(expr.Y)
		ce(err)
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

func (a *Account) Top() *Account {
	ret := a
	for ret.Parent != nil && ret.Parent.Parent != nil {
		ret = ret.Parent
	}
	return ret
}

func (a *Account) MatchPath(path []string) bool {
	c := a
	for i := len(path) - 1; i >= 0; i-- {
		if c.Name == path[i] {
			c = c.Parent
		}
	}
	return c.Parent == nil // is root
}

func parseDate(str string) time.Time {
	str = strings.Replace(str, "/", "-", -1)
	str = strings.Replace(str, ".", "-", -1)
	t, err := time.Parse("2006-01-02", str)
	ce(err, "bad date: %s", str)
	return t
}

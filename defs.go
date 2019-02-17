package main

var (
	itemKinds = map[string]bool{
		"数码":   true,
		"物品":   true,
		"衣物服饰": true,
		"书籍":   true,
	}

	consumableKinds = map[string]bool{
		"消耗品": true,
		"保健品": true,
		"药物":  true,
	}

	sortWeight = map[sortWeightKey]int{
		{1, "保险"}:  1,
		{1, "消耗品"}: 2,
		{1, "负债"}:  3,
		{1, "资产"}:  4,
	}

	noSkipZeroAmountAccounts = map[string]bool{
		"保险":  true,
		"消耗品": true,
		"物品":  true,
	}
)

type sortWeightKey struct {
	Level int
	Name  string
}

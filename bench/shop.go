package main

// The benchmark project: a small Go shop library with tests, plus one seeded bug per coding task.
// Deterministic, so every run and both arms see byte-identical files.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type module struct{ name, code string }

var modules = []module{
	{"pricing", `// Package shop holds the shop's pricing, stock, cart, order and report logic.
package shop

import "math"

// TaxRate is the sales tax rate added by AddTax.
const TaxRate = 0.18

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// ApplyDiscount returns the price after a fractional discount, where 0.2 means 20% off.
func ApplyDiscount(price, discount float64) float64 {
	return round2(price * (1 - discount))
}

// AddTax returns the price including tax at the given rate, normally TaxRate.
func AddTax(price, rate float64) float64 {
	return round2(price * (1 + rate))
}

// BulkPrice lowers the unit price by 5% from 10 units and by 10% from 50 units.
func BulkPrice(unitPrice float64, quantity int) float64 {
	if quantity >= 50 {
		return round2(unitPrice * 0.90)
	}
	if quantity >= 10 {
		return round2(unitPrice * 0.95)
	}
	return unitPrice
}
`},
	{"inventory", `package shop

import "fmt"

// Inventory tracks how many units of each item are in stock.
type Inventory struct {
	Stock map[string]int
}

// NewInventory returns an empty inventory.
func NewInventory() *Inventory {
	return &Inventory{Stock: map[string]int{}}
}

// Add puts units of an item into stock.
func (inv *Inventory) Add(item string, quantity int) {
	inv.Stock[item] += quantity
}

// Remove takes units out of stock, refusing to go negative.
func (inv *Inventory) Remove(item string, quantity int) error {
	if inv.Stock[item] < quantity {
		return fmt.Errorf("not enough %s in stock", item)
	}
	inv.Stock[item] -= quantity
	return nil
}

// NeedsRestock reports whether the stock of an item is at or below the threshold.
func (inv *Inventory) NeedsRestock(item string, threshold int) bool {
	return inv.Stock[item] <= threshold
}
`},
	{"cart", `package shop

// Line is one entry in a cart.
type Line struct {
	Name  string
	Price float64
	Qty   int
}

// Cart is a shopping cart.
type Cart struct {
	Items []Line
}

// Add puts a line item in the cart.
func (c *Cart) Add(name string, price float64, qty int) {
	c.Items = append(c.Items, Line{Name: name, Price: price, Qty: qty})
}

// Total is the sum of price times quantity over all lines, after any cart-wide discount.
func (c *Cart) Total(discount float64) float64 {
	raw := 0.0
	for _, l := range c.Items {
		raw += l.Price * float64(l.Qty)
	}
	return ApplyDiscount(raw, discount)
}
`},
	{"orders", `package shop

import "time"

// Statuses is an order's life cycle, in order.
var Statuses = []string{"new", "paid", "shipped", "delivered"}

// Order is one customer order.
type Order struct {
	ID     int
	Total  float64
	Status string
	Date   time.Time
}

// MakeOrder creates an order in the new state, dated today when when is zero.
func MakeOrder(id int, total float64, when time.Time) Order {
	if when.IsZero() {
		when = time.Now()
	}
	return Order{ID: id, Total: total, Status: "new", Date: when}
}

// Advance moves an order to the next status; delivered orders stay delivered.
func Advance(o *Order) {
	for i, s := range Statuses {
		if s == o.Status {
			o.Status = Statuses[min(i+1, len(Statuses)-1)]
			return
		}
	}
}
`},
	{"reports", `package shop

import "fmt"

// MonthlySummary totals order value per month, keyed by year-month such as 2026-03.
func MonthlySummary(orders []Order) map[string]float64 {
	out := map[string]float64{}
	for _, o := range orders {
		key := fmt.Sprintf("%d-%02d", o.Date.Year(), int(o.Date.Month()))
		out[key] = round2(out[key] + o.Total)
	}
	return out
}
`},
}

var tests = map[string]string{
	"pricing_test.go": `package shop

import "testing"

func TestApplyDiscount(t *testing.T) {
	if got := ApplyDiscount(100, 0.2); got != 80 {
		t.Fatalf("ApplyDiscount(100, 0.2) = %v, want 80", got)
	}
}

func TestAddTax(t *testing.T) {
	if got := AddTax(100, TaxRate); got != 118 {
		t.Fatalf("AddTax(100, TaxRate) = %v, want 118", got)
	}
}

func TestBulkPrice(t *testing.T) {
	if got := BulkPrice(10, 50); got != 9 {
		t.Fatalf("BulkPrice(10, 50) = %v, want 9", got)
	}
}
`,
	"inventory_test.go": `package shop

import "testing"

func TestNeedsRestockAtThreshold(t *testing.T) {
	inv := NewInventory()
	inv.Add("pen", 5)
	if !inv.NeedsRestock("pen", 5) {
		t.Fatal("5 pens at threshold 5 should need restocking")
	}
	if inv.NeedsRestock("pen", 4) {
		t.Fatal("5 pens at threshold 4 should not need restocking")
	}
}
`,
	"cart_test.go": `package shop

import "testing"

func TestTotalCountsQuantity(t *testing.T) {
	var c Cart
	c.Add("pen", 2.0, 3)
	c.Add("book", 10.0, 1)
	if got := c.Total(0); got != 16 {
		t.Fatalf("Total = %v, want 16", got)
	}
}
`,
	"reports_test.go": `package shop

import (
	"testing"
	"time"
)

func TestMonthlySummary(t *testing.T) {
	orders := []Order{
		MakeOrder(1, 10.0, time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)),
		MakeOrder(2, 5.5, time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)),
	}
	got := MonthlySummary(orders)
	if len(got) != 1 || got["2026-03"] != 15.5 {
		t.Fatalf("MonthlySummary = %v, want map[2026-03:15.5]", got)
	}
}
`,
}

type bug struct{ name, file, clean, buggy string }

// bugs are applied to one fresh copy of the shop per coding task, in this order.
var bugs = []bug{
	{"discount", "pricing.go", "price * (1 - discount)", "price * discount"},
	{"restock", "inventory.go", "<= threshold", "< threshold"},
	{"cart", "cart.go", "l.Price * float64(l.Qty)", "l.Price"},
	{"month", "reports.go", "int(o.Date.Month())", "int(o.Date.Month())-1"},
}

func bugNames() []string {
	out := make([]string, len(bugs))
	for i, b := range bugs {
		out[i] = b.name
	}
	return out
}

func findBug(name string) (bug, bool) {
	for _, b := range bugs {
		if b.name == name {
			return b, true
		}
	}
	return bug{}, false
}

// helpers adds plain utility functions so every file is a realistic size, not a toy.
func helpers(name string, count int) string {
	seed := 0
	for _, c := range name {
		seed += int(c)
	}
	title := strings.ToUpper(name[:1]) + name[1:]
	var b strings.Builder
	for i := range count {
		a, m, k := (seed*(i+3))%17+2, (seed+i*7)%29+11, i%5+3
		fmt.Fprintf(&b, `
// %sRule%d normalises a value according to %s rule %d.
func %sRule%d(value int) int {
	total := 0
	for step := 0; step < %d; step++ {
		total += (value*%d + step) %% %d
	}
	if total > %d {
		total -= %d
	}
	return total
}
`, title, i, name, i, title, i, k, a, m, m*k/2, k)
	}
	return b.String()
}

// build writes the shop to root, with the named bug seeded when bugName is not empty.
func build(root, bugName string) (string, error) {
	var seeded *bug
	if bugName != "" {
		b, ok := findBug(bugName)
		if !ok {
			return "", fmt.Errorf("unknown bug %q (have %s)", bugName, strings.Join(bugNames(), ", "))
		}
		seeded = &b
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	files := map[string]string{"go.mod": "module shop\n\ngo 1.21\n"}
	for _, m := range modules {
		code := m.code
		if seeded != nil && seeded.file == m.name+".go" {
			if strings.Count(code, seeded.clean) != 1 {
				return "", fmt.Errorf("bug %s: %q must appear once in %s", seeded.name, seeded.clean, seeded.file)
			}
			code = strings.Replace(code, seeded.clean, seeded.buggy, 1)
		}
		files[m.name+".go"] = code + helpers(m.name, 14)
	}
	for name, code := range tests {
		files[name] = code
	}
	for name, code := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(code), 0o644); err != nil {
			return "", err
		}
	}
	return root, nil
}

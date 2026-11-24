package profanity

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

type tc struct {
	text string
	root string // expected root of the first match, "" = must be clean
}

func runCases(t *testing.T, cases []tc) {
	t.Helper()
	for _, c := range cases {
		res := Check(c.text)
		if c.root == "" {
			if res.Profane {
				t.Errorf("false positive: %q → %+v", c.text, res.Matches)
			}
			continue
		}
		if !res.Profane {
			t.Errorf("missed: %q (want root %q)", c.text, c.root)
			continue
		}
		if res.Matches[0].Root != c.root {
			t.Errorf("%q: root %q, want %q", c.text, res.Matches[0].Root, c.root)
		}
	}
}

func TestRootHui(t *testing.T) {
	runCases(t, []tc{
		{"хуй", "хуй"}, {"Нахуя ты это сделал?", "хуй"}, {"похуй", "хуй"},
		{"охуенно", "хуй"}, {"хуёвый день", "хуй"}, {"нихуя себе", "хуй"},
		{"хуйня какая-то", "хуй"}, {"схуяли", "хуй"}, {"понахуярили тут", "хуй"},
		{"ахуеть", "хуй"}, {"хуесос", "хуй"}, {"хуило", "хуй"},
		// traps
		{"застрахуй машину", ""}, {"Застрахуйте имущество", ""}, {"страхую жизнь", ""},
		{"тихую ночь", ""}, {"сухую погоду", ""}, {"плохую новость", ""},
		{"глухую стену", ""}, {"лихую тройку", ""}, {"худой хутор", ""}, {"похудеть", ""}, {"исхудать", ""}, {"ухудшение", ""}, {"Хуан приехал", ""},
	})
}

func TestRootPizd(t *testing.T) {
	runCases(t, []tc{
		{"Полный пиздец на дорогах", "пизд"}, {"пизда", "пизд"}, {"спиздил", "пизд"},
		{"распиздяй", "пизд"}, {"пиздёж", "пизд"}, {"пёзды", "пизд"},
		{"пицца и писатель", ""}, {"поздно", ""}, {"пиздрик", "пизд"},
	})
}

func TestRootEb(t *testing.T) {
	runCases(t, []tc{
		{"Не еби мозг", "еб"}, {"ебать", "еб"}, {"заебал", "еб"}, {"ЗАЕБАЛО", "еб"},
		{"ёбаный стыд", "еб"}, {"уебок", "еб"}, {"выебываться", "еб"}, {"еблан", "еб"},
		{"ебучий будильник", "еб"}, {"съебался", "еб"}, {"отъебись", "еб"},
		{"долбоёб", "еб"}, {"подъебать", "еб"}, {"разъебал", "еб"}, {"проебали", "еб"},
		{"наебнулся", "еб"}, {"ёб твою мать", "еб"}, {"приёбываться", "еб"},
		// traps
		{"Учебник по физике", ""}, {"потребитель", ""}, {"Употребление сахара", ""},
		{"небо", ""}, {"хлеб", ""}, {"себе и тебе", ""}, {"веб-сайт", ""}, {"ребёнок", ""}, {"ебонит", ""}, {"Себастьян", ""}, {"въезд", ""},
		{"требовать", ""}, {"дебил", ""}, {"обед", ""}, {"двоеборье", ""}, {"нахлебник", ""},
		{"убежище", ""}, {"погребальный", ""}, {"пребывание", ""}, {"гребень", ""},
	})
}

func TestRootBlya(t *testing.T) {
	runCases(t, []tc{
		{"Бля, опоздал", "бля"}, {"блядь", "бля"}, {"блять", "бля"}, {"бляди", "бля"},
		{"блядство", "бля"}, {"выблядок", "бля"},
		// traps
		{"Курс рубля", ""}, {"сабля", ""}, {"корабля", ""}, {"оскорблять", ""},
		{"употреблять", ""}, {"колеблясь", ""}, {"ослаблять", ""}, {"бляха с ремня", ""},
		{"бляшки", ""}, {"блат", ""}, {"блин", ""}, {"ансамбля", ""},
	})
}

func TestRootMudak(t *testing.T) {
	runCases(t, []tc{
		{"Какой мудак тут припарковался", "мудак"}, {"мудила", "мудак"}, {"мудачьё", "мудак"},
		{"мудозвон", "мудак"}, {"мудило", "мудак"},
		{"мудрый совет", ""}, {"премудрость", ""}, {"мудрёный", ""},
	})
}

func TestRootPidor(t *testing.T) {
	runCases(t, []tc{
		{"пидор", "пидор"}, {"пидар", "пидор"}, {"пидорас", "пидор"}, {"пидарасы", "пидор"},
		{"пидрила", "пидор"}, {"спидорасило", "пидор"},
		{"скипидар", ""}, {"Скипидарная мазь", ""}, {"спидометр", ""}, {"пиджак", ""},
	})
}

func TestRootZalup(t *testing.T) {
	runCases(t, []tc{
		{"залупа", "залуп"}, {"не залупайся", "залуп"},
		{"за лупой", ""}, {"лупа", ""}, {"залп", ""},
	})
}

func TestEnglish(t *testing.T) {
	runCases(t, []tc{
		{"fuck", "fuck"}, {"Fucking hell", "fuck"}, {"motherfucker", "fuck"},
		{"Shit happens", "shit"}, {"bullshit", "shit"}, {"bitch", "bitch"}, {"cunt", "cunt"},
		{"fuuuuck", "fuck"}, {"sh1t", "shit"}, {"b1tch", "bitch"}, {"$hit", "shit"},
		{"f*ck", "fuck"}, {"f u c k", "fuck"},
		// traps
		{"Scunthorpe United", ""}, {"shiitake mushrooms", ""}, {"shitake", ""}, {"assassin", ""},
		{"Let's hit the road", ""}, {"U.S. hit record", ""}, {"he's hitting", ""},
		{"Fukushima", ""}, {"hue and saturation", ""}, {"buy on ebay", ""}, {"ebook", ""},
		{"EBITDA grew", ""}, {"Matsushita and Yamashita", ""}, {"a spider", ""}, {"mudra", ""}, {"pizza", ""},
	})
}

func TestTranslit(t *testing.T) {
	runCases(t, []tc{
		{"pizdec", "пизд"}, {"blyat", "бля"}, {"blyad", "бля"}, {"nahuy", "хуй"},
		{"zaebal", "еб"}, {"yobany", "еб"}, {"mudak", "мудак"}, {"pidor", "пидор"},
		{"xuy", "хуй"}, {"pi3da", "пизд"}, {"zalupa", "залуп"},
	})
}

func TestMaskHomoglyphs(t *testing.T) {
	runCases(t, []tc{
		{"xуй", "хуй"}, {"хyй", "хуй"}, {"пиzдец", "пизд"}, {"eбать", "еб"},
		{"6лядь", "бля"}, {"бл@ть", "бля"}, {"пи3дец", "пизд"}, {"пuздец", "пизд"},
		{"mудак", "мудак"}, {"пидоp", "пидор"}, {"зaлупa", "залуп"}, {"}{уй", "хуй"},
		{"hуй", "хуй"}, {"п1зда", "пизд"}, {"ХУЙ", "хуй"}, {"ПиЗдЕц", "пизд"},
		{"ёбаный", "еб"}, {"Ебаный", "еб"}, {"fуck", "fuck"},
		// Latin letters inside clean words must not create matches.
		{"кoманда", ""}, {"рубль 100", ""}, {"c6ербанк", ""},
	})
}

func TestMaskSeparators(t *testing.T) {
	runCases(t, []tc{
		{"х у й", "хуй"}, {"х.у.й", "хуй"}, {"п-и-з-д-а", "пизд"}, {"x у й с ним", "хуй"},
		{"б_л_я", "бля"}, {"е б а т ь", "еб"}, {"ху й", "хуй"}, {"х*у*й", "хуй"},
		{"п.и.з.д.е.ц", "пизд"}, {"х. у. й.", "хуй"},
		// traps: single letters / abbreviations that must not join into words
		{"и т.д. и т.п.", ""}, {"А. С. Пушкин", ""}, {"в у к а", ""}, {"а я и ты", ""},
	})
}

func TestMaskRepeatsAndStars(t *testing.T) {
	runCases(t, []tc{
		{"бляяяять", "бля"}, {"ббббля", "бля"}, {"хууууй", "хуй"}, {"пиииздец", "пизд"},
		{"заеееебал", "еб"}, {"п*здец", "пизд"}, {"х*й", "хуй"}, {"бл*ть", "бля"},
		{"ебанный", "еб"},
		{"к*манда", ""}, {"*важно*", ""}, {"рубляяя", ""},
	})
}

func TestTraps(t *testing.T) {
	runCases(t, []tc{
		{"Наша команда лучшая", ""}, {"Мандарины в подарок", ""}, {"оскорблять", ""},
		{"употреблять", ""}, {"застрахуй", ""}, {"скипидар", ""}, {"учебник", ""},
		{"потребитель", ""}, {"рубля", ""}, {"сабля", ""}, {"Scunthorpe", ""},
		{"shiitake", ""}, {"assassin", ""}, {"Хлеб всему голова", ""},
		{"Художественная литература", ""}, {"Небесно-голубой цвет", ""}, {"", ""},
	})
}

func TestMatchPositions(t *testing.T) {
	res := Check("ну ёб твою, хуйня")
	if len(res.Matches) != 2 {
		t.Fatalf("matches: %+v", res.Matches)
	}
	want := []Match{
		{Word: "ёб", Start: 3, End: 5, Root: "еб"},
		{Word: "хуйня", Start: 12, End: 17, Root: "хуй"},
	}
	for i, w := range want {
		if res.Matches[i] != w {
			t.Errorf("match %d = %+v, want %+v", i, res.Matches[i], w)
		}
	}
	if r := Check("чистый текст"); r.Profane || r.Matches == nil || len(r.Matches) != 0 {
		t.Errorf("clean text: %+v", r)
	}
}

func TestCensor(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Полный пиздец на дорогах", "Полный п*** на дорогах"},
		{"Бля, опоздал", "Б***, опоздал"},
		{"Наша команда лучшая", "Наша команда лучшая"},
		{"x у й с ним", "x*** с ним"},
		{"ЗАЕБАЛО, ёб твою", "З***, ё*** твою"},
		{"what the fuck, man", "what the f***, man"},
		{"пи3дец и п-и-з-д-а", "п*** и п***"},
		{"Курс рубля и сабля", "Курс рубля и сабля"},
		{"ну бл.я.дь!", "ну б***!"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Censor(c.in); got != c.want {
			t.Errorf("Censor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestHardSet runs testdata/hard.tsv, the hand-written hard examples.
func TestHardSet(t *testing.T) {
	f, err := os.Open("../testdata/hard.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		label, text, ok := strings.Cut(sc.Text(), "\t")
		if !ok {
			continue
		}
		n++
		if got := Check(text).Profane; got != (label == "1") {
			t.Errorf("hard.tsv: %q: profane=%v, want label %s (%+v)", text, got, label, Check(text).Matches)
		}
	}
	if n < 30 {
		t.Errorf("hard.tsv has %d examples, want >= 30", n)
	}
}

func BenchmarkCheck(b *testing.B) {
	lines := []string{
		"Наша команда выиграла чемпионат, поздравляем всех болельщиков!",
		"Полный пиздец на дорогах, опять пробка до самого центра",
		"x у й с ним, завтра разберёмся",
		"Shit happens, but the shiitake soup was great in Scunthorpe",
	}
	for i := 0; b.Loop(); i++ {
		Check(lines[i%len(lines)])
	}
}

package seed

// name is a (romanized, native-script) pair; native is empty for Latin-script locales.
type name struct{ latin, native string }

type locale struct {
	code        string // language code for books
	countries   []string
	weight      int
	firsts      []name
	lasts       []name
	familyFirst bool     // CJK order: family name first in native script
	words       []string // title vocabulary
	joiner      string   // separator between title words ("" for CJK)
}

func n(l string) name     { return name{latin: l} }
func nn(l, x string) name { return name{latin: l, native: x} }

var locales = []locale{
	{code: "en", countries: []string{"US", "GB", "CA", "AU", "IE", "NZ"}, weight: 34,
		firsts: []name{n("James"), n("Mary"), n("John"), n("Patricia"), n("Robert"), n("Jennifer"), n("Michael"), n("Linda"), n("William"), n("Elizabeth"), n("David"), n("Susan"), n("Ernest"), n("Virginia"), n("George"), n("Charlotte")},
		lasts:  []name{n("Smith"), n("Johnson"), n("Williams"), n("Brown"), n("Jones"), n("Miller"), n("Davis"), n("Wilson"), n("Taylor"), n("Clark"), n("Hemingway"), n("Austen"), n("Woolf"), n("Orwell"), n("Dickens"), n("Bronte")},
		words:  []string{"Shadow", "River", "Night", "Garden", "Empire", "Silent", "Last", "Secret", "Journey", "Winter", "Stone", "Light", "House", "Memory", "War", "Peace", "Dream", "City", "Ocean", "Fire", "Star", "King", "Lost", "Golden", "Forgotten", "Island", "Letters", "Song", "Road", "Time", "of", "the", "and"},
		joiner: " "},
	{code: "ru", countries: []string{"RU", "BY", "KZ"}, weight: 12,
		firsts: []name{nn("Ivan", "Иван"), nn("Aleksey", "Алексей"), nn("Maria", "Мария"), nn("Anna", "Анна"), nn("Dmitry", "Дмитрий"), nn("Olga", "Ольга"), nn("Sergey", "Сергей"), nn("Natalya", "Наталья"), nn("Nikolay", "Николай"), nn("Tatyana", "Татьяна"), nn("Mikhail", "Михаил"), nn("Elena", "Елена"), nn("Fyodor", "Фёдор"), nn("Lev", "Лев")},
		lasts:  []name{nn("Ivanov", "Иванов"), nn("Petrov", "Петров"), nn("Smirnov", "Смирнов"), nn("Kuznetsov", "Кузнецов"), nn("Popov", "Попов"), nn("Sokolov", "Соколов"), nn("Lebedev", "Лебедев"), nn("Kozlov", "Козлов"), nn("Novikov", "Новиков"), nn("Morozov", "Морозов"), nn("Volkov", "Волков"), nn("Tolstoy", "Толстой"), nn("Chekhov", "Чехов"), nn("Pushkin", "Пушкин"), nn("Dostoevsky", "Достоевский"), nn("Bulgakov", "Булгаков")},
		words:  []string{"тень", "река", "ночь", "сад", "империя", "тихий", "последний", "тайна", "путешествие", "зима", "камень", "свет", "дом", "память", "война", "мир", "сон", "город", "океан", "огонь", "звезда", "король", "потерянный", "золотой", "остров", "письма", "песня", "дорога", "время", "сердце", "и"},
		joiner: " "},
	{code: "zh", countries: []string{"CN", "TW", "SG"}, weight: 10, familyFirst: true,
		firsts: []name{nn("Wei", "伟"), nn("Fang", "芳"), nn("Na", "娜"), nn("Min", "敏"), nn("Jing", "静"), nn("Li", "丽"), nn("Qiang", "强"), nn("Lei", "磊"), nn("Jun", "军"), nn("Yang", "洋"), nn("Xun", "迅"), nn("Mo", "莫")},
		lasts:  []name{nn("Wang", "王"), nn("Li", "李"), nn("Zhang", "张"), nn("Liu", "刘"), nn("Chen", "陈"), nn("Yang", "杨"), nn("Zhao", "赵"), nn("Huang", "黄"), nn("Zhou", "周"), nn("Wu", "吴"), nn("Lu", "鲁"), nn("Ba", "巴")},
		words:  []string{"红", "楼", "梦", "山", "水", "月", "风", "花", "雪", "夜", "春", "秋", "江", "湖", "天", "下", "人", "间", "心", "城", "海", "星", "光", "影", "路", "家", "国", "书", "剑", "龙"},
		joiner: ""},
	{code: "ja", countries: []string{"JP"}, weight: 6, familyFirst: true,
		firsts: []name{nn("Taro", "太郎"), nn("Hanako", "花子"), nn("Ken", "健"), nn("Yumi", "由美"), nn("Haruki", "春樹"), nn("Yasunari", "康成"), nn("Naoko", "直子"), nn("Sho", "翔"), nn("Yukio", "由紀夫"), nn("Banana", "ばなな")},
		lasts:  []name{nn("Sato", "佐藤"), nn("Suzuki", "鈴木"), nn("Takahashi", "高橋"), nn("Tanaka", "田中"), nn("Murakami", "村上"), nn("Kawabata", "川端"), nn("Watanabe", "渡辺"), nn("Ito", "伊藤"), nn("Mishima", "三島"), nn("Yoshimoto", "吉本")},
		words:  []string{"夜", "桜", "海", "辺", "の", "物語", "風", "森", "猫", "雪国", "春", "夢", "光", "影", "東京", "島", "歌", "月", "空", "花"},
		joiner: ""},
	{code: "ar", countries: []string{"EG", "SA", "LB", "MA", "SY", "IQ"}, weight: 6,
		firsts: []name{nn("Muhammad", "محمد"), nn("Ahmad", "أحمد"), nn("Fatima", "فاطمة"), nn("Ali", "علي"), nn("Naguib", "نجيب"), nn("Layla", "ليلى"), nn("Omar", "عمر"), nn("Sara", "سارة"), nn("Khalil", "خليل"), nn("Mahmoud", "محمود")},
		lasts:  []name{nn("Mahfouz", "محفوظ"), nn("Hussein", "حسين"), nn("Al-Khatib", "الخطيب"), nn("Al-Ali", "العلي"), nn("Mansour", "منصور"), nn("Haddad", "حداد"), nn("Said", "سعيد"), nn("Gibran", "جبران"), nn("Darwish", "درويش"), nn("Qabbani", "قباني")},
		words:  []string{"الظل", "النهر", "الليل", "الحديقة", "الإمبراطورية", "الصامت", "الأخير", "السر", "الرحلة", "الشتاء", "الحجر", "النور", "البيت", "الذاكرة", "الحرب", "السلام", "الحلم", "المدينة", "البحر", "النار", "و"},
		joiner: " "},
	{code: "es", countries: []string{"ES", "MX", "AR", "CO", "CL", "PE"}, weight: 10,
		firsts: []name{n("José"), n("María"), n("Carlos"), n("Lucía"), n("Gabriel"), n("Sofía"), n("Miguel"), n("Isabel"), n("Jorge"), n("Pablo")},
		lasts:  []name{n("García"), n("Rodríguez"), n("González"), n("Fernández"), n("López"), n("Martínez"), n("Sánchez"), n("Pérez"), n("Márquez"), n("Cervantes"), n("Borges"), n("Neruda")},
		words:  []string{"sombra", "río", "noche", "jardín", "imperio", "silencio", "último", "secreto", "viaje", "invierno", "piedra", "luz", "casa", "memoria", "guerra", "paz", "sueño", "ciudad", "océano", "fuego", "estrella", "rey", "de", "la", "y"},
		joiner: " "},
	{code: "fr", countries: []string{"FR", "BE", "CH", "CA"}, weight: 8,
		firsts: []name{n("Jean"), n("Marie"), n("Pierre"), n("Camille"), n("Louis"), n("Chloé"), n("Émile"), n("Hélène"), n("Victor"), n("Albert")},
		lasts:  []name{n("Martin"), n("Bernard"), n("Dubois"), n("Thomas"), n("Robert"), n("Petit"), n("Durand"), n("Lefèvre"), n("Hugo"), n("Zola"), n("Camus"), n("Proust")},
		words:  []string{"ombre", "rivière", "nuit", "jardin", "empire", "silence", "dernier", "secret", "voyage", "hiver", "pierre", "lumière", "maison", "mémoire", "guerre", "paix", "rêve", "ville", "océan", "feu", "étoile", "roi", "de", "la", "et"},
		joiner: " "},
	{code: "de", countries: []string{"DE", "AT", "CH"}, weight: 8,
		firsts: []name{n("Hans"), n("Anna"), n("Karl"), n("Greta"), n("Jürgen"), n("Lena"), n("Fritz"), n("Sophie"), n("Thomas"), n("Hermann")},
		lasts:  []name{n("Müller"), n("Schmidt"), n("Schneider"), n("Fischer"), n("Weber"), n("Meyer"), n("Wagner"), n("Becker"), n("Goethe"), n("Mann"), n("Hesse"), n("Kafka")},
		words:  []string{"Schatten", "Fluss", "Nacht", "Garten", "Reich", "Stille", "letzte", "Geheimnis", "Reise", "Winter", "Stein", "Licht", "Haus", "Erinnerung", "Krieg", "Frieden", "Traum", "Stadt", "Ozean", "Feuer", "Stern", "König", "der", "und"},
		joiner: " "},
	{code: "ko", countries: []string{"KR"}, weight: 3, familyFirst: true,
		firsts: []name{nn("Min-jun", "민준"), nn("Seo-yeon", "서연"), nn("Ji-ho", "지호"), nn("Han", "강"), nn("Eun-ji", "은지")},
		lasts:  []name{nn("Kim", "김"), nn("Lee", "이"), nn("Park", "박"), nn("Choi", "최"), nn("Han", "한")},
		words:  []string{"바다", "밤", "꽃", "바람", "별", "사랑", "기억", "도시", "겨울", "노래"},
		joiner: " "},
	{code: "hi", countries: []string{"IN"}, weight: 3,
		firsts: []name{nn("Amit", "अमित"), nn("Priya", "प्रिया"), nn("Rahul", "राहुल"), nn("Anita", "अनीता"), nn("Premchand", "प्रेमचंद")},
		lasts:  []name{nn("Sharma", "शर्मा"), nn("Verma", "वर्मा"), nn("Gupta", "गुप्ता"), nn("Singh", "सिंह"), nn("Kumar", "कुमार")},
		words:  []string{"रात", "नदी", "सपना", "घर", "आकाश", "प्रेम", "युद्ध", "शहर", "गीत", "समय"},
		joiner: " "},
}

var genres = []string{"fiction", "fantasy", "science-fiction", "mystery", "thriller", "romance", "history", "biography",
	"poetry", "drama", "philosophy", "science", "mathematics", "children", "young-adult", "horror", "classics",
	"religion", "travel", "cooking", "art", "economics", "politics", "psychology", "self-help"}

var publisherStems = []string{"Penguin", "Harper", "Random", "Oxford", "Cambridge", "Eksmo", "AST", "Gallimard", "Suhrkamp",
	"Planeta", "Kodansha", "Shinchosha", "Dar al-Shorouk", "Zhonghua", "Springer", "Wiley", "Hachette", "Macmillan",
	"Bloomsbury", "Vintage", "Azbuka", "Anagrama", "Fischer", "Minumsa"}

var publisherSuffixes = []string{"", " Press", " Books", " Publishing", " House"}

var editions = []string{"1st", "2nd", "3rd", "revised", "anniversary", "illustrated", "paperback"}

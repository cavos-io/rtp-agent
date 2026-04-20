package language

import (
	"strings"
)

var ISO639_3_To_1 = map[string]string{
	"afr": "af", "amh": "am", "ara": "ar", "hye": "hy", "asm": "as", "aze": "az",
	"bel": "be", "ben": "bn", "bos": "bs", "bul": "bg", "mya": "my", "cat": "ca",
	"cmn": "zh", "nya": "ny", "hrv": "hr", "ces": "cs", "dan": "da", "nld": "nl",
	"eng": "en", "est": "et", "fin": "fi", "fra": "fr", "ful": "ff", "glg": "gl",
	"lug": "lg", "kat": "ka", "deu": "de", "ell": "el", "guj": "gu", "hau": "ha",
	"heb": "he", "hin": "hi", "hun": "hu", "isl": "is", "ibo": "ig", "ind": "id",
	"gle": "ga", "ita": "it", "jpn": "ja", "jav": "jv", "kan": "kn", "kaz": "kk",
	"khm": "km", "kor": "ko", "kur": "ku", "kir": "ky", "lao": "lo", "lav": "lv",
	"lin": "ln", "lit": "lt", "ltz": "lb", "mkd": "mk", "msa": "ms", "mal": "ml",
	"mlt": "mt", "zho": "zh", "mri": "mi", "mar": "mr", "mon": "mn", "nep": "ne",
	"nor": "no", "oci": "oc", "ori": "or", "pus": "ps", "fas": "fa", "pol": "pl",
	"por": "pt", "pan": "pa", "ron": "ro", "rus": "ru", "srp": "sr", "sna": "sn",
	"snd": "sd", "slk": "sk", "slv": "sl", "som": "so", "spa": "es", "swa": "sw",
	"swe": "sv", "tam": "ta", "tgk": "tg", "tel": "te", "tha": "th", "tur": "tr",
	"ukr": "uk", "urd": "ur", "uzb": "uz", "vie": "vi", "cym": "cy", "wol": "wo",
	"xho": "xh", "zul": "zu",
}

var LanguageNamesToCode = map[string]string{
	"afrikaans": "af", "albanian": "sq", "amharic": "am", "arabic": "ar",
	"armenian": "hy", "azerbaijani": "az", "basque": "eu", "belarusian": "be",
	"bengali": "bn", "bosnian": "bs", "bulgarian": "bg", "burmese": "my",
	"catalan": "ca", "chinese": "zh", "croatian": "hr", "czech": "cs",
	"danish": "da", "dutch": "nl", "english": "en", "estonian": "et",
	"finnish": "fi", "french": "fr", "galician": "gl", "georgian": "ka",
	"german": "de", "greek": "el", "gujarati": "gu", "hausa": "ha",
	"hebrew": "he", "hindi": "hi", "hungarian": "hu", "icelandic": "is",
	"indonesian": "id", "irish": "ga", "italian": "it", "japanese": "ja",
	"javanese": "jv", "kannada": "kn", "kazakh": "kk", "khmer": "km",
	"korean": "ko", "kurdish": "ku", "kyrgyz": "ky", "lao": "lo",
	"latvian": "lv", "lingala": "ln", "lithuanian": "lt", "luxembourgish": "lb",
	"macedonian": "mk", "malay": "ms", "malayalam": "ml", "maltese": "mt",
	"maori": "mi", "marathi": "mr", "mongolian": "mn", "nepali": "ne",
	"norwegian": "no", "occitan": "oc", "oriya": "or", "pashto": "ps",
	"persian": "fa", "polish": "pl", "portuguese": "pt", "punjabi": "pa",
	"romanian": "ro", "russian": "ru", "serbian": "sr", "shona": "sn",
	"sindhi": "sd", "slovak": "sk", "slovene": "sl", "slovenian": "sl",
	"somali": "so", "spanish": "es", "swahili": "sw", "swedish": "sv",
	"tagalog": "tl", "tamil": "ta", "tajik": "tg", "telugu": "te",
	"thai": "th", "turkish": "tr", "ukrainian": "uk", "urdu": "ur",
	"uzbek": "uz", "vietnamese": "vi", "welsh": "cy", "wolof": "wo",
	"xhosa": "xh", "yoruba": "yo", "zulu": "zu",
}

var CodeToLanguageName = make(map[string]string)

func init() {
	for name, code := range LanguageNamesToCode {
		CodeToLanguageName[code] = name
	}
	CodeToLanguageName["sl"] = "slovene"
}

// NormalizeLanguage normalizes a language code/name to BCP-47 format.
func NormalizeLanguage(code string) string {
	lowered := strings.TrimSpace(strings.ToLower(code))

	// Check language names first
	if val, ok := LanguageNamesToCode[lowered]; ok {
		return val
	}

	// Check ISO 639-3
	if val, ok := ISO639_3_To_1[lowered]; ok {
		return val
	}

	// Handle BCP-47 with region (e.g. "en-US")
	lowered = strings.ReplaceAll(lowered, "_", "-")
	parts := strings.Split(lowered, "-")

	if len(parts) >= 2 {
		lang := parts[0]
		normalizedParts := []string{lang}

		for _, part := range parts[1:] {
			if len(part) == 4 {
				// Script subtag
				normalizedParts = append(normalizedParts, strings.Title(strings.ToLower(part)))
			} else {
				// Region subtag
				normalizedParts = append(normalizedParts, strings.ToUpper(part))
			}
		}
		return strings.Join(normalizedParts, "-")
	}

	return lowered
}


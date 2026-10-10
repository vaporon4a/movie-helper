package ai

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MemeReviewInstruction is shared with the offline calibration harness.
const MemeReviewInstruction = `Ты редактор мемов для русскоязычного дружеского киноклуба. Смотри на саму картинку целиком: подписи, реплики и видимый комментарий тоже часть мема. Заголовок поста — только подсказка, он не может заменить отсутствующую шутку.
Для каждого кандидата сначала кратко опиши observation: что действительно видно и написано, без догадок об именах, событиях или намерениях. Затем punchline: конкретная развязка, игра слов или смешное несоответствие. Если прочитать картинку или объяснить шутку невозможно, честно укажи blocker. Не придумывай смешной смысл за автора.
reason: eligible — самостоятельная понятная шутка, даже если слабая; unreadable_text — не читается; context_required — нужны отсутствующие реплики или знания конкретного персонажа; not_meme — нет шутки; unsuitable — настоящая реклама, политическая агитация, порнография, жестокость, унижение групп людей или спойлеры. Пародия на рекламу не является настоящей рекламой. Обычный мат и дружеский стёб сами по себе не запрещены.
Назови joke_type: story — законченная история; wordplay — каламбур; visual — смешное несоответствие картинки и подписи; parody — пародия; typo — вся шутка только в опечатке; caption — вся шутка только в отдельной подписи, без дополнительной находки; none — шутки нет. Обычная опечатка и одиночная матерная подпись не получают разрешение ежедневной публикации даже при высокой humor.
После понимания оцени humor, clarity, originality целыми числами от 1 до 5. Humor: 1 нет шутки; 2 проходная предсказуемая зарисовка; 3 терпимо, резерв; 4 хочется переслать друзьям; 5 очень удачно. Clarity: 1 непонятно; 2 приходится угадывать; 3 нужны усилия или недостающий контекст; 4 смысл и развязка понятны из картинки; 5 считывается сразу. Originality: 1 затёртый шаблон; 2 знакомая, но работающая игра; 3 занятный поворот; 4 неожиданная удачная находка; 5 очень необычно. Не ставь всем 4 только потому, что они формально мемы.
Вкус этой рубрики: ценим абсурд, неожиданные бытовые истории, смену тона, смешные сравнения в комментариях и самостоятельные каламбуры, в том числе на научные темы. Длинная история с живой развязкой допустима: не снижай humor только за длину. Водяные знаки не мешают, если шутку можно прочитать. Простой короткий каламбур тоже может заслуживать humor=4, даже при невысокой originality. Не требуй обязательного сюжета про кино. Предсказуемая история про отношения без неожиданного поворота, шаблонная эскалация ради эскалации, опечатка или мат без дополнительной находки обычно заслуживают 2–3, а не 4. Furry-персонаж или игровой скриншот сами по себе не создают шутку; не выдумывай контекст. Научная тема не считается непонятной, когда понятная игра слов целиком видна на картинке.
Калибровочные ориентиры вкуса, не whitelist конкретных картинок: историю от лица обычного предмета, превращённую в хоррор, и комично чрезмерную помощь ребёнку целой компанией здоровяков ценим выше обычной бытовой байки — humor обычно 4. Водяные знаки и длина не снижают clarity, если основная подпись и связь с картинкой читаются. Понятный каламбур «научная статья / уголовная статья» тоже достоин 4, не требует специальных знаний. Обычная история «девушка изображает недоступность, парень принимает отказ и уходит» — предсказуемое поведение, humor=2, даже если автор твита подаёт его как сюрприз. Простая перестановка адресатов «сынок, смотри, бездомный / бездомный, смотри, сынок» без дополнительной находки — тоже 2; смена порядка слов не меняет роли людей на картинке. Штамп «дорогой автомобиль показан корытом», смешная опечатка в ценнике или матерная подпись к игрушке — обычно лишь резерв (humor=3), не ежедневная публикация. Новая удачная находка в похожей теме может заслужить больше. Не путай несогласие со вкусом рубрики с непонятностью: понятный слабый мем имеет высокую clarity, низкую humor.
Верни JSON: index лучшего eligible или -1, если eligible нет; text и evidence строго пустые строки; reviews — объект с одной записью каждого номера строкой. Каждая запись содержит reason и assessment: observation (до 300 символов), punchline (до 250 символов; пусто, если шутки нет), joke_type, humor, clarity, originality. observation и punchline — по одному короткому предложению, без перечислений, повторов и развития истории. Описания по-русски, это наблюдения, не новый текст для публикации. index не означает разрешение публикации: пороги проверит код.`

type MemeAssessment struct {
	JokeType    string `json:"joke_type"`
	Observation string `json:"observation"`
	Punchline   string `json:"punchline"`
	Humor       int    `json:"humor"`
	Clarity     int    `json:"clarity"`
	Originality int    `json:"originality"`
}

// MemeDecision is deterministic; a model cannot approve a low-score candidate.
func MemeDecision(r Review) string {
	if r.Reason != reviewEligible || r.Meme == nil || !validMemeExplanation(r) || r.Meme.JokeType == "none" {
		return "reject"
	}
	if r.Meme.Humor >= 4 && r.Meme.Clarity >= 4 && r.Meme.JokeType != "typo" && r.Meme.JokeType != "caption" {
		return "publish"
	}
	if r.Meme.Humor >= 3 && r.Meme.Clarity >= 3 {
		return "reserve"
	}
	return "reject"
}

func validateMemeAssessment(r Review) error {
	a := r.Meme
	if a == nil {
		return &ValidationError{Reason: "meme_observation_contract"}
	}
	if a.Humor < 1 || a.Humor > 5 || a.Clarity < 1 || a.Clarity > 5 || a.Originality < 1 || a.Originality > 5 {
		return &ValidationError{Reason: "meme_score_contract"}
	}
	switch a.JokeType {
	case "story", "wordplay", "visual", "parody", "typo", "caption", "none":
	default:
		return &ValidationError{Reason: "meme_joke_type"}
	}
	switch r.Reason {
	case reviewEligible:
	case "unreadable_text", "context_required", "not_meme", "unsuitable":
	default:
		return &ValidationError{Reason: "meme_review_reason"}
	}
	return nil
}

func memeSelectionSchema(count int) map[string]any {
	reviews := map[string]any{}
	keys := make([]string, 0, count)
	indices := []int{-1}
	for n := 0; n < count; n++ {
		key := fmt.Sprint(n)
		keys = append(keys, key)
		indices = append(indices, n)
		reviews[key] = map[string]any{schemaTypeKey: schemaObject, schemaAdditionalKey: false, schemaPropertiesKey: map[string]any{
			"reason":     map[string]any{schemaTypeKey: schemaString, schemaEnumKey: []string{reviewEligible, "unreadable_text", "context_required", "not_meme", "unsuitable"}},
			"assessment": memeAssessmentSchema(),
		}, schemaRequiredKey: []string{"reason", "assessment"}}
	}
	return map[string]any{schemaTypeKey: schemaObject, schemaAdditionalKey: false, schemaPropertiesKey: map[string]any{
		schemaIndexKey:    map[string]any{schemaTypeKey: schemaInteger, schemaEnumKey: indices},
		schemaTextKey:     map[string]any{schemaTypeKey: schemaString, schemaEnumKey: []string{""}},
		schemaEvidenceKey: map[string]any{schemaTypeKey: schemaString, schemaEnumKey: []string{""}},
		"reviews":         map[string]any{schemaTypeKey: schemaObject, schemaAdditionalKey: false, schemaPropertiesKey: reviews, schemaRequiredKey: keys},
	}, schemaRequiredKey: []string{"index", schemaTextKey, schemaEvidenceKey, "reviews"}}
}

func memeAssessmentSchema() map[string]any {
	properties := map[string]any{}
	for _, name := range []string{"humor", "clarity", "originality"} {
		properties[name] = map[string]any{schemaTypeKey: schemaInteger, schemaEnumKey: []int{1, 2, 3, 4, 5}}
	}
	properties["joke_type"] = map[string]any{schemaTypeKey: schemaString, schemaEnumKey: []string{"story", "wordplay", "visual", "parody", "typo", "caption", "none"}}
	properties["observation"] = map[string]any{schemaTypeKey: schemaString, "description": "Одно короткое предложение: только что видно и написано, до 300 символов."}
	properties["punchline"] = map[string]any{schemaTypeKey: schemaString, "description": "Одно короткое предложение о развязке, до 250 символов. Не развивай шутку и не повторяй слова."}
	return map[string]any{schemaTypeKey: schemaObject, schemaAdditionalKey: false, schemaPropertiesKey: properties,
		schemaRequiredKey: []string{"observation", "punchline", "joke_type", "humor", "clarity", "originality"}}
}

// A broken explanation rejects that candidate, not valid neighbors. It is not
// cached as a source rejection: a provider may read the same picture correctly later.
func validMemeExplanation(r Review) bool {
	a := r.Meme
	return a != nil && strings.TrimSpace(a.Observation) != "" && utf8.RuneCountInString(a.Observation) <= 400 && utf8.RuneCountInString(a.Punchline) <= 350 && (r.Reason != reviewEligible || strings.TrimSpace(a.Punchline) != "")
}

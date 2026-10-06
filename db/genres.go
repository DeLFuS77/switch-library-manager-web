package db

import (
	"sort"
	"strings"
)

// The titles database mixes the genres of several stores: "Action", "アクション", "액션" and
// "動作" are the same genre. They are turned into these English names, which the interface
// translates; genres that are none of them are left out.
var Genres = []string{"Action", "Adventure", "Arcade", "Board Game", "Communication", "Education", "Fighting",
	"First-Person Shooter", "Lifestyle", "Multiplayer", "Music", "Other", "Party", "Platformer", "Practical", "Puzzle",
	"Racing", "RPG", "Shooter", "Simulation", "Sports", "Strategy", "Study", "Training", "Utility", "Video"}

var genreNames = map[string]string{
	// Japanese
	"アクション": "Action", "アドベンチャー": "Adventure", "アーケード": "Arcade", "テーブル": "Board Game", "コミュニケーション": "Communication",
	"格闘": "Fighting", "音楽": "Music", "その他": "Other", "パーティー": "Party", "実用": "Practical", "パズル": "Puzzle", "レース": "Racing",
	"ロールプレイング": "RPG", "シューティング": "Shooter", "シミュレーション": "Simulation", "スポーツ": "Sports", "ストラテジー": "Strategy",
	"学習": "Study", "トレーニング": "Training",
	// Korean
	"액션": "Action", "어드벤처": "Adventure", "아케이드": "Arcade", "격투": "Fighting", "음악": "Music", "기타": "Other", "파티": "Party",
	"퍼즐": "Puzzle", "레이싱": "Racing", "롤플레잉": "RPG", "슈팅": "Shooter", "시뮬레이션": "Simulation", "스포츠": "Sports", "전략": "Strategy",
	"학습": "Study", "트레이닝": "Training",
	// Chinese
	"動作": "Action", "动作": "Action", "冒險": "Adventure", "冒险": "Adventure", "角色扮演": "RPG", "益智": "Puzzle", "模擬": "Simulation",
	"模拟": "Simulation", "其他": "Other", "射擊": "Shooter", "射击": "Shooter", "策略": "Strategy", "運動": "Sports", "运动": "Sports",
	// French
	"Jeu de société": "Board Game",
}

var knownGenres = func() map[string]string {
	known := make(map[string]string, len(Genres)+len(genreNames))
	for _, genre := range Genres {
		known[strings.ToLower(genre)] = genre
	}
	for name, genre := range genreNames {
		known[strings.ToLower(name)] = genre
	}
	return known
}()

// normalizeGenres returns the genres of a title in their English names, sorted, without
// repeating any.
func normalizeGenres(categories []string, intern func(string) string) []string {
	if len(categories) == 0 {
		return nil
	}
	set := map[string]bool{}
	for _, category := range categories {
		if genre, ok := knownGenres[strings.ToLower(strings.TrimSpace(category))]; ok {
			set[genre] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	genres := make([]string, 0, len(set))
	for genre := range set {
		genres = append(genres, intern(genre))
	}
	sort.Strings(genres)
	return genres
}

// normalizeLanguages returns the languages of a title as lower case codes, sorted, without
// repeating any (the database lists Chinese twice: simplified and traditional).
func normalizeLanguages(languages []string, intern func(string) string) []string {
	if len(languages) == 0 {
		return nil
	}
	set := map[string]bool{}
	for _, language := range languages {
		language = strings.ToLower(strings.TrimSpace(language))
		if len(language) >= 2 && len(language) <= 5 {
			set[language] = true
		}
	}
	codes := make([]string, 0, len(set))
	for code := range set {
		codes = append(codes, intern(code))
	}
	sort.Strings(codes)
	return codes
}

// normalizeTitles cleans the genres and languages of the titles and shares their strings,
// which repeat in tens of thousands of titles. DLC keep none of them.
func normalizeTitles(result *SwitchTitlesDB) {
	pool := map[string]string{}
	intern := func(value string) string {
		if shared, ok := pool[value]; ok {
			return shared
		}
		pool[value] = value
		return value
	}
	for _, title := range result.TitlesMap {
		attributes := &title.Attributes
		attributes.Genres = normalizeGenres(attributes.Genres, intern)
		attributes.Languages = normalizeLanguages(attributes.Languages, intern)
		for i, content := range attributes.RatingContent {
			attributes.RatingContent[i] = intern(content)
		}
		if attributes.Publisher != "" {
			attributes.Publisher = intern(attributes.Publisher)
		}
		for id, dlc := range title.Dlc {
			if dlc.Genres != nil || dlc.Languages != nil || dlc.RatingContent != nil || dlc.Intro != "" {
				dlc.Genres, dlc.Languages, dlc.RatingContent, dlc.Intro = nil, nil, nil, ""
				title.Dlc[id] = dlc
			}
		}
	}
}

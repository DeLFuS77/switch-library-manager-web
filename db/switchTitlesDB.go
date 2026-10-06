package db

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

type TitleAttributes struct {
	Id          string      `json:"id"`
	Name        string      `json:"name,omitempty"`
	Version     json.Number `json:"version,omitempty"`
	Region      string      `json:"region,omitempty"`
	ReleaseDate int         `json:"releaseDate,omitempty"`
	Publisher   string      `json:"publisher,omitempty"`
	IconUrl     string      `json:"iconUrl,omitempty"`
	Screenshots []string    `json:"screenshots,omitempty"`
	BannerUrl   string      `json:"bannerUrl,omitempty"`
	Description string      `json:"description,omitempty"`
	Size        int         `json:"size,omitempty"`
	IsDemo      bool        `json:"isDemo,omitempty"`
	// genres (see Genres), number of players, language codes and age rating of the game
	Genres        []string `json:"category,omitempty"`
	Players       int      `json:"numberOfPlayers,omitempty"`
	Languages     []string `json:"languages,omitempty"`
	AgeRating     int      `json:"rating,omitempty"`
	RatingContent []string `json:"ratingContent,omitempty"`
	// a short sentence about the game, in English
	Intro string `json:"intro,omitempty"`
}

type SwitchTitle struct {
	Attributes TitleAttributes
	Updates    map[int]string
	Dlc        map[string]TitleAttributes
}

type SwitchTitlesDB struct {
	TitlesMap map[string]*SwitchTitle
	// names and descriptions per interface language, by upper case title ID
	Localized map[string]map[string]LocalizedTitle
}

// LocalizedTitle returns the name and description of a title in a language, if known.
func (s *SwitchTitlesDB) LocalizedTitle(lang string, titleId string) (LocalizedTitle, bool) {
	if s == nil || s.Localized == nil {
		return LocalizedTitle{}, false
	}
	title, ok := s.Localized[lang][strings.ToUpper(titleId)]
	return title, ok
}

// CreateSwitchTitleDB builds the titles database from titles.json and versions.json.
// titles.json is over 100 MB: it is read entry by entry instead of being decoded into one
// big map first, which halves the memory needed, and the descriptions and screenshots of
// DLC, never shown, are dropped.
func CreateSwitchTitleDB(titlesFile, versionsFile io.Reader) (*SwitchTitlesDB, error) {
	//titleID -> versionId-> release date
	var versions = map[string]map[int]string{}
	if err := decodeToJsonObject(versionsFile, &versions); err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bufio.NewReaderSize(titlesFile, 1<<20))
	if token, err := decoder.Token(); err != nil {
		return nil, err
	} else if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("titles database: not a JSON object")
	}

	result := SwitchTitlesDB{TitlesMap: map[string]*SwitchTitle{}}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("titles database: unexpected key")
		}
		var attr TitleAttributes
		if err := decoder.Decode(&attr); err != nil {
			return nil, err
		}

		id := strings.ToLower(key)
		idPrefix, err := titleIDPrefix(id)
		if err != nil {
			zap.S().Debugf("skipping unsupported title ID %q: %v", id, err)
			continue
		}

		//TitleAttributes id rules:
		//main TitleAttributes ends with 000
		//Updates ends with 800
		//Dlc adds 1 to the 4th char from the right and have a running counter
		//(starting with 001) in the 3 last chars
		switchTitle, ok := result.TitlesMap[idPrefix]
		if !ok {
			switchTitle = &SwitchTitle{Dlc: map[string]TitleAttributes{}}
			result.TitlesMap[idPrefix] = switchTitle
		}

		//process Updates
		if strings.HasSuffix(id, "800") {
			switchTitle.Updates = versions[id[0:len(id)-3]+"000"]
			continue
		}

		//process main TitleAttributes
		if strings.HasSuffix(id, "000") {
			switchTitle.Attributes = attr
			continue
		}

		//not an update, and not main TitleAttributes, so treat it as a DLC
		attr.Description = ""
		attr.Screenshots = nil
		switchTitle.Dlc[id] = attr
	}

	normalizeTitles(&result)
	return &result, nil
}

// TitleIDPrefix returns the key that groups a base game with its update and DLC title IDs,
// as used by SwitchTitlesDB.TitlesMap and LocalSwitchFilesDB.TitlesMap.
func TitleIDPrefix(id string) (string, error) {
	return titleIDPrefix(id)
}

// titleIDPrefix returns the key that groups a base game with its update and DLC title IDs.
// Ported from https://github.com/trembon/switch-library-manager
func titleIDPrefix(id string) (string, error) {
	id = strings.ToLower(id)
	if len(id) != 16 {
		return "", errors.New("title ID must contain 16 hexadecimal characters")
	}
	if _, err := strconv.ParseUint(id, 16, 64); err != nil {
		return "", errors.New("title ID must contain 16 hexadecimal characters")
	}
	if strings.HasSuffix(id, "000") || strings.HasSuffix(id, "800") {
		return id[:len(id)-3], nil
	}
	value, _ := strconv.ParseUint(id[len(id)-4:len(id)-3], 16, 4)
	if value == 0 {
		return "", errors.New("DLC title ID has an invalid group nibble")
	}
	return id[:len(id)-4] + strconv.FormatUint(value-1, 16), nil
}

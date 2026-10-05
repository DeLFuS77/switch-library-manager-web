package web

import (
	"encoding/json"
	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/gorilla/mux"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

type ApiFileInfo struct {
	DownloadUrl string `json:"downloadUrl,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Type        string `json:"type,omitempty"`
}

type ApiSystemInfo struct {
	RequiredSystemVersion int `json:"requiredSystemVersion,omitempty"`
}

type ApiExtendedFileInfo struct {
	ApiFileInfo
	DisplayVersion string `json:"displayVersion,omitempty"`
	Version        int    `json:"version"`
}

type ApiUpdateItem struct {
	ApiExtendedFileInfo
	ApiSystemInfo
}

type ApiDlcItem struct {
	ApiExtendedFileInfo
	Name                       string `json:"name"`
	RequiredApplicationVersion int    `json:"requiredApplicationVersion,omitempty"`
}

type ApiTitleItem struct {
	ApiFileInfo
	ApiSystemInfo
	BannerUrl     string                `json:"bannerUrl,omitempty"`
	IconUrl       string                `json:"iconUrl,omitempty"`
	ThumbnailUrl  string                `json:"thumbnailUrl,omitempty"`
	LatestUpdate  ApiUpdateItem         `json:"latestUpdate"`
	Name          map[string]string     `json:"name"`
	Region        string                `json:"region,omitempty"`
	Dlc           map[string]ApiDlcItem `json:"dlc,omitempty"`
}

func fileType(fileName string) string {
	return strings.ToUpper(strings.TrimPrefix(filepath.Ext(fileName), "."))
}

// findLocalTitle returns the local title whose base file has the given title ID.
func findLocalTitle(localDB *db.LocalSwitchFilesDB, titleId string) *db.SwitchGameFiles {
	if localDB == nil {
		return nil
	}
	for _, v := range localDB.TitlesMap {
		if v.BaseExist && v.File.Metadata != nil && strings.EqualFold(v.File.Metadata.TitleId, titleId) {
			return v
		}
	}
	return nil
}

func serveDownload(w http.ResponseWriter, r *http.Request, file db.ExtendedFileInfo) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=" + strconv.Quote(file.FileName))
	http.ServeFile(w, r, filepath.Join(file.BaseFolder, file.FileName))
}

func (web *Web) HandleApi() {
	web.router.HandleFunc("/api/titles", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		items := map[string]ApiTitleItem{}
		switchDB, localDB := web.state.get()

		if localDB != nil {
			for k, v := range localDB.TitlesMap {
				if v.BaseExist && v.File.Metadata != nil {
					var switchTitle *db.SwitchTitle
					if switchDB != nil {
						switchTitle = switchDB.TitlesMap[k]
					}

					titleId := strings.ToUpper(v.File.Metadata.TitleId)
					latestUpdate := ApiUpdateItem{ ApiExtendedFileInfo: ApiExtendedFileInfo { Version: v.LatestUpdate } }
					name := map[string]string{}

					if v.File.Metadata.Ncap != nil {
						latestUpdate.DisplayVersion = v.File.Metadata.Ncap.DisplayVersion
					}

					if update, ok := v.Updates[v.LatestUpdate]; ok && update.Metadata != nil {
						latestUpdate.DownloadUrl = "/api/titles/" + titleId + "/updates/" + strconv.Itoa(v.LatestUpdate)
						latestUpdate.Size = update.ExtendedInfo.Size
						latestUpdate.Type = fileType(update.ExtendedInfo.FileName)
						latestUpdate.RequiredSystemVersion = update.Metadata.RequiredTitleVersion

						if update.Metadata.Ncap != nil {
							latestUpdate.DisplayVersion = update.Metadata.Ncap.DisplayVersion
						}
					}

					if v.File.Metadata.Ncap != nil {
						for _, langV := range v.File.Metadata.Ncap.TitleName {
							if langV.Title != "" {
								name[langV.Language.ToLanguageTag()] = langV.Title
							}
						}
					}

					if len(name) == 0 {
						name["unknown"] = db.ParseTitleNameFromFileName(v.File.ExtendedInfo.FileName)
					}

					items[titleId] = ApiTitleItem {
						ApiFileInfo:   ApiFileInfo {
							DownloadUrl:  "/api/titles/" + titleId,
							Size:         v.File.ExtendedInfo.Size,
							Type:         fileType(v.File.ExtendedInfo.FileName),
						},
						ApiSystemInfo: ApiSystemInfo {
							RequiredSystemVersion:        v.File.Metadata.RequiredTitleVersion,
						},
						LatestUpdate:  latestUpdate,
						Name:          name,
					}

					if switchTitle != nil {
						if item, ok2 := items[titleId]; ok2 {
							item.Region = switchTitle.Attributes.Region
							items[titleId] = item
						}
					}

					if item, ok1 := items[titleId]; ok1 {
						if v.Banner != "" {
							item.BannerUrl = "/i/" + v.Banner
						}

						if v.Icon != "" {
							item.IconUrl = "/i/" + v.Icon
						}

						if item.IconUrl != "" {
							item.ThumbnailUrl = item.IconUrl + "?width=90"
						} else if item.BannerUrl != "" {
							item.ThumbnailUrl = item.BannerUrl + "?width=90"
						}

						item.Dlc = map[string]ApiDlcItem{}

						for id, dlc := range v.Dlc {
							if dlc.Metadata == nil {
								continue
							}
							dlcTitleId := strings.ToUpper(id)

							item.Dlc[dlcTitleId] = ApiDlcItem {
								ApiExtendedFileInfo:        ApiExtendedFileInfo {
									ApiFileInfo:    ApiFileInfo {
										DownloadUrl: "/api/titles/" + titleId + "/dlc/" + dlcTitleId,
										Size:        dlc.ExtendedInfo.Size,
										Type:        fileType(dlc.ExtendedInfo.FileName),
									},
									Version:        dlc.Metadata.Version,
								},
								RequiredApplicationVersion: dlc.Metadata.RequiredTitleVersion,
							}

							if switchTitle != nil {
								if entry, ok2 := switchTitle.Dlc[id]; ok2 {
									if dlcItem, ok3 := item.Dlc[dlcTitleId]; ok3 {
										dlcItem.Name = entry.Name
										item.Dlc[dlcTitleId] = dlcItem
									}
								}
							}

							if entry, ok2 := item.Dlc[dlcTitleId]; ok2 {
								if dlc.Metadata.Ncap != nil {
									entry.DisplayVersion = dlc.Metadata.Ncap.DisplayVersion
								}

								item.Dlc[dlcTitleId] = entry
							}
						}

						items[titleId] = item
					}
				}
			}
		}

		json.NewEncoder(w).Encode(items)
	})

	web.router.HandleFunc("/api/titles/{titleId}", func(w http.ResponseWriter, r *http.Request) {
		_, localDB := web.state.get()
		if title := findLocalTitle(localDB, mux.Vars(r)["titleId"]); title != nil {
			serveDownload(w, r, title.File.ExtendedInfo)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	})

	web.router.HandleFunc("/api/titles/{titleId}/updates/{version}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		_, localDB := web.state.get()

		if version, err := strconv.Atoi(vars["version"]); err == nil {
			if title := findLocalTitle(localDB, vars["titleId"]); title != nil {
				if update, ok := title.Updates[version]; ok {
					serveDownload(w, r, update.ExtendedInfo)
					return
				}
			}
		}

		w.WriteHeader(http.StatusNotFound)
	})

	web.router.HandleFunc("/api/titles/{titleId}/dlc/{dlcTitleId}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		_, localDB := web.state.get()

		if title := findLocalTitle(localDB, vars["titleId"]); title != nil {
			for id, dlc := range title.Dlc {
				if strings.EqualFold(id, vars["dlcTitleId"]) {
					serveDownload(w, r, dlc.ExtendedInfo)
					return
				}
			}
		}

		w.WriteHeader(http.StatusNotFound)
	})
}

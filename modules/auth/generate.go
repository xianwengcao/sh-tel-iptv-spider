package auth

import (
	"encoding/xml"
	"errors"
	"fmt"
	"iptv-spider-sh/config"
	"iptv-spider-sh/global"
	"iptv-spider-sh/model"
	"iptv-spider-sh/modules/m3u"
	"iptv-spider-sh/utils"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/golang-module/carbon"
)

const timeFormat = carbon.ShortDateTimeLayout + " -0700"

func GenerateM3u8(udpxy, scheme, xteve, all string) []byte {
	m3uWriter := m3u.NewWriter()
	m3uWriter.WriteHeaderWithInfo(global.CONFIG.Epg.XmlUrl)            //加载配置文件参数，
	fmt.Println("ChannelMappings:", global.CONFIG.Epg.ChannelMappings) //确认配置是否加载调试

	// 查询数据库
	var channelInfoList []model.ChannelInfo

	global.DB.Order("mix_no asc").Find(&channelInfoList)
	fmt.Println("查询到的频道数量:", len(channelInfoList))
	// 去重
	newChanInfo := model.RemoveDuplicateChannelInfo(channelInfoList)
	// 输出去重后的频道数量和信息
	fmt.Println("去重后的频道数量:", len(newChanInfo))
	//fmt.Println("去重后的频道信息:", newChanInfo)

	// 构建映射表
	mappingMap := make(map[string]config.ChannelMapping)
	for _, m := range global.CONFIG.Epg.ChannelMappings {
		mappingMap[m.Igmp] = m
	}

	// 构建最终列表：先加 channel_infos，再加未匹配的 ChannelMappings
	type M3uItem struct {
		Info    model.ChannelInfo
		Channel model.Channel
		Mapping *config.ChannelMapping
	}

	var finalList []M3uItem
	processed := make(map[string]bool)
	for _, info := range newChanInfo {
		if !info.IsShow {
			continue
		}
		channel := model.Channel{}
		global.DB.Where("user_channel_id = ?", info.MixNo).Find(&channel)

		key := fmt.Sprintf("%v", channel.ChannelURL)
		processed[key] = true

		var mapping *config.ChannelMapping
		if m, ok := mappingMap[channel.ChannelURL]; ok {
			mapping = &m
		}

		finalList = append(finalList, M3uItem{
			Info:    info,
			Channel: channel,
			Mapping: mapping,
		})
	}

	// 加入 ChannelMappings 中未匹配的 IGMP 频道
	for _, m := range global.CONFIG.Epg.ChannelMappings {
		if _, ok := processed[m.Igmp]; !ok {
			channel := model.Channel{}
			// 使用 IGMP 去 channels 表匹配 channel_url
			global.DB.Where("channel_url = ?", m.Igmp).Find(&channel)

			info := model.ChannelInfo{
				MixNo:    m.Id,
				CommName: m.Name,
				Name:     m.Name,
				IsShow:   true,
			}
			finalList = append(finalList, M3uItem{
				Info:    info,
				Channel: channel,
				Mapping: &m,
			})
		}
	}
	// ✅ 构建 name_sequence 顺序表
	orderMap := make(map[string]int)
	for i, m := range global.CONFIG.Epg.ChannelMappings {
		if m.Name_sequence != "" {
			orderMap[m.Name_sequence] = i
		}
	}

	// 对 finalList 排序
	sort.SliceStable(finalList, func(i, j int) bool {
		nameI := finalList[i].Info.Name
		nameJ := finalList[j].Info.Name

		indexI, okI := orderMap[nameI]
		indexJ, okJ := orderMap[nameJ]

		// 如果都在 name_sequence 中，按顺序表排序
		if okI && okJ {
			return indexI < indexJ
		}

		// 如果只有 i 在顺序表，i 优先
		if okI {
			return true
		}

		// 如果只有 j 在顺序表，j 优先
		if okJ {
			return false
		}

		// 都不在顺序表，保持原数据库顺序（按 MixNo）
		numI, errI := strconv.Atoi(finalList[i].Info.MixNo)
		numJ, errJ := strconv.Atoi(finalList[j].Info.MixNo)
		if errI != nil || errJ != nil {
			return i < j // 出错按原顺序
		}
		return numI < numJ
	})
	// ✅ 统一循环写入 m3u
	for _, item := range finalList {
		info := item.Info
		channel := item.Channel
		m3u8Mapping := model.M3u8Mapping{}
		//针对手动配置用户数据库判断分组
		global.DB.Where("comm_name = ?", info.CommName).Find(&m3u8Mapping)
		if m3u8Mapping.AutoGroups == "" {
			m3u8Mapping.AutoGroups = autoGroupByName(info.Name)
		}

		// 默认 logo
		if m3u8Mapping.Logo == "" {
			logoBaseUrl := global.CONFIG.Epg.LogoUrl
			logoImageName := fmt.Sprintf("%s.png", info.CommName)
			m3u8Mapping.Logo = fmt.Sprintf("%s%s", logoBaseUrl, logoImageName)
		}

		// 用户自定义映射覆盖
		if item.Mapping != nil {
			if item.Mapping.Name != "" {
				info.CommName = item.Mapping.Name

			}
			if item.Mapping.Logo != "" {
				m3u8Mapping.Logo = global.CONFIG.Epg.LogoUrl + item.Mapping.Logo
			}

		}

		uri := assemblyUrl(udpxy, scheme, xteve, channel.ChannelURL, channel.ChannelFCCIP, channel.ChannelFCCPort)

		catchupSource := ""
		if channel.TimeShiftURL != "" {
			trimmed := strings.TrimPrefix(channel.TimeShiftURL, "rtsp://")
			catchupSource = fmt.Sprintf("%s%s%s", global.CONFIG.Epg.RtspUrl, trimmed, global.CONFIG.Epg.Playseek)
		}

		m3uWriter.WriteWithCatchup(uri, catchupSource, info, m3u8Mapping)
	}

	return m3uWriter.Bytes()
}

func GenerateTimeShiftM3u8() []byte {
	m3uWriter := m3u.NewWriter()
	m3uWriter.WriteHeaderWithInfo(global.CONFIG.Epg.XmlUrl)
	// 查询数据库
	var channelInfoList []model.ChannelInfo
	global.DB.Find(&channelInfoList)
	// 去重
	newChanInfo := model.RemoveDuplicateChannelInfo(channelInfoList)
	for _, info := range newChanInfo {
		// 不展示
		if !info.IsShow {
			continue
		}
		channel := model.Channel{}
		global.DB.Where("user_channel_id = ?", info.MixNo).
			Find(&channel)

		m3u8Mapping := model.M3u8Mapping{}
		global.DB.Where("comm_name = ?", info.CommName).
			Find(&m3u8Mapping)

		uri := assemblyUrl("", "", "", channel.TimeShiftURL, "", "") //修改加上fcc端口和用户
		m3uWriter.Write(uri, info, m3u8Mapping)
	}
	return m3uWriter.Bytes()
}

// func assemblyUrl(udpxy, scheme, xteve, uri string) string //修改
func assemblyUrl(udpxy, scheme, xteve, uri, fccIp, fccPort string) string {
	u, _ := url.Parse(uri)

	// xteve模式
	if xteve == "true" {
		return fmt.Sprintf("udp://@%s", u.Host)
	}

	// udpxy模式
	if udpxy != "" {
		return fmt.Sprintf("http://%s/udp/%s", udpxy, u.Host)
	}

	// HTTP RTP + FCC 使用动态加载的 rtp_url
	if fccIp != "" && fccPort != "" {
		return fmt.Sprintf(
			"%s%s?fcc=%s:%s",
			global.CONFIG.Epg.RtpUrl, // 使用动态加载的 rtp_url
			u.Host,
			fccIp,
			fccPort,
		)
	}

	// HTTP RTP 无FCC 使用动态加载的 rtp_url
	return fmt.Sprintf(
		"%s%s",
		global.CONFIG.Epg.RtpUrl, // 使用动态加载的 rtp_url
		u.Host,
	)
}

func GenerateXmlTv(daysAgo int) ([]byte, error) {
	if daysAgo < 1 {
		daysAgo = 1
	} else if daysAgo > 7 {
		daysAgo = 7
	}
	var now = carbon.Now()
	var xmlTv = model.XmlTV{
		Generator: fmt.Sprintf("%s %s", global.CONFIG.Epg.Generator, now.ToDateTimeString()),
		Source:    global.CONFIG.Epg.Source,
	}
	// 取数据
	var channelInfoList []model.ChannelInfo
	global.DB.Find(&channelInfoList)
	// 去重
	newChanInfo := model.RemoveDuplicateChannelInfo(channelInfoList)
	for _, info := range newChanInfo {
		// 不展示
		if !info.IsShow {
			continue
		}
		chId := info.MixNo
		xmlTv.Channel = append(xmlTv.Channel, &model.XmlTvChannel{
			ID:          chId,
			DisplayName: []model.DisplayName{{Lang: "zh", Value: info.CommName}},
		})
		if !info.IsPullEPG {
			xmlTv.Program = append(xmlTv.Program, &model.Program{
				Channel: chId,
				Title:   []*model.Title{{Lang: "zh"}},
				Desc:    []*model.Desc{{Lang: "zh"}},
			})
			continue
		}

		var epgData []model.EPGDetails
		global.DB.Where("comm_name = ?", info.CommName).
			Where("end_time > ?", now.SubDays(daysAgo).TimestampMilli()).
			Order("start_time asc").
			Find(&epgData)

		for _, epg := range epgData {
			startTime := carbon.CreateFromTimestampMilli(epg.StartTime).Layout(timeFormat)
			endTime := carbon.CreateFromTimestampMilli(epg.EndTime).Layout(timeFormat)
			xmlTv.Program = append(xmlTv.Program, &model.Program{
				Channel: chId,
				Start:   startTime,
				Stop:    endTime,
				Title:   []*model.Title{{Lang: "zh", Value: epg.Name}},
				Desc:    []*model.Desc{{Lang: "zh"}},
			})
		}
	}
	// 序列化
	epgBytes, err := xml.MarshalIndent(&xmlTv, "", "  ")
	if err != nil {
		global.LOG.Error("节目表单生成出错: " + err.Error())
		return nil, errors.New("节目表单生成出错")
	}
	epgBytes = append([]byte(model.PrefixHeader+"\n"), epgBytes...)
	return epgBytes, nil
}

func GenerateAndUploadM3u() {
	m3uBytes := GenerateM3u8("", "", "true", "")
	utils.UploadToOSS("/sh/tel-xteve.m3u", m3uBytes)
}

func GenerateAndUploadXmlTv() {
	xmlTvBytes, _ := GenerateXmlTv(1)
	utils.UploadToOSS("/sh/tel-epg.xml", xmlTvBytes)
}

func GenerateAndUploadXmlTvDays7() {
	xmlTvBytes, _ := GenerateXmlTv(7)
	utils.UploadToOSS("/sh/tel-epg-7.xml", xmlTvBytes)
}
func autoGroupByName(name string) string {
	if strings.Contains(name, "CCTV") {
		return "央视"
	} else if strings.Contains(name, "卫视") {
		return "卫视"
	} else if strings.Contains(name, "购物") {
		return "购物"
	} else if strings.Contains(name, "年级") {
		return "空中课堂"
	} else if strings.Contains(name, "百事通") {
		return "百事通"
	} else if strings.Contains(name, "影") {
		return "电影"
	}
	return "其他"
}

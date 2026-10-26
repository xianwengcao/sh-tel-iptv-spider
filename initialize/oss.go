package initialize

import (
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/tencentyun/cos-go-sdk-v5"
	"iptv-spider-sh/global"
	"net/http"
	"net/url"
)

func COS() *cos.Client {
	cosConfig := global.CONFIG.OSS
	if !cosConfig.Enable {
		return nil
	}
	u, err := url.Parse(cosConfig.EndPoint)
	if err != nil {
		panic("存储地址解析失败！")
	}
	c := cos.NewClient(&cos.BaseURL{BucketURL: u}, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:  cosConfig.AccessKey,
			SecretKey: cosConfig.SecretKey,
			// 出站网卡绑定：让上传也从指定网卡出去；未启用时为 nil，沿用默认行为
			Transport: global.BIND.OSS.Transport(),
		},
	})
	return c
}

func Minio() *minio.Client {
	minioConfig := global.CONFIG.OSS
	if !minioConfig.Enable {
		return nil
	}
	_, err := url.Parse(minioConfig.EndPoint)
	if err != nil {
		panic("存储地址解析失败！")
	}
	c, err := minio.New(minioConfig.EndPoint, &minio.Options{
		Creds:        credentials.NewStaticV4(minioConfig.AccessKey, minioConfig.SecretKey, ""),
		Secure:       minioConfig.UseSSL,
		BucketLookup: minio.BucketLookupDNS,
		// 出站网卡绑定：让上传也从指定网卡出去；未启用时为 nil，沿用 minio 默认传输
		Transport: global.BIND.OSS.Transport(),
	})
	if err != nil {
		return nil
	}
	return c
}

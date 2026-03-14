#!/bin/bash
set -e

# 项目名称
PROJECT_NAME="sh-tel-iptv-spider"

# 打印版本信息（可选）
echo "Building $PROJECT_NAME, version: $VERSION"

# 创建 build 目录（防止不存在报错）
mkdir -p build

# 使用 gox 交叉编译，多平台输出到 build 目录
# 增加 Windows 版本
CGO_ENABLED=0 gox -ldflags "-s -w ${LDFLAGS}" \
  -output="build/${PROJECT_NAME}_{{.OS}}_{{.Arch}}" \
  --osarch="darwin/amd64 darwin/arm64 linux/386 linux/amd64 linux/arm linux/arm64 windows/386 windows/amd64"

# 进入 build 目录
cd build

# 生成多种校验和
rhash -r -a . -o checksums
rhash -r -a --bsd . -o checksums-bsd
rhash --list-hashes > checksums_hashes_order

echo "Build and checksum generation completed!"

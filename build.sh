#!/bin/bash
export PLAYFAST_SUDO="pass"

# 安装 Wails
go install github.com/wailsapp/wails/v2/cmd/wails@latest
# 获取最新标签（按版本降序排序，并取第一个）
latest_tag=$(git tag --sort=-v:refname | head -n 1)

if [ -n "$latest_tag" ]; then
    echo "Latest Git Tags: $latest_tag"
else
    latest_tag="v1.0.0"
    echo "Labels were not found in the repository. Use default tags: $latest_tag"
fi

# 构建项目
wails build -clean -ldflags "-s -w -X main.Version=$latest_tag" -platform darwin/arm64 -tags "with_gvisor,with_clash_api" -trimpath -webview2 embed

# 打包dmg
brew install create-dmg
create-dmg --app-drop-link 300 150 --icon-size 42 --volname "PlayFast" build/bin/PlayFast.dmg build/bin/PlayFast.app

# 计算 SHA-256
file="build/bin/PlayFast.app/Contents/MacOS/PlayFast"
if [ -f "$file" ]; then
    echo "SHA-256:"
    shasum -a 256 "$file"
else
    echo "找不到文件: $file"
fi

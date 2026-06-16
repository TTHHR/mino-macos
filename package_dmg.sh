#!/bin/bash
# mino - DMG 打包脚本
# 用法: ./package_dmg.sh          # 构建当前架构（ARM64 for M芯片）
#       ./package_dmg.sh intel    # 构建 Intel 兼容版

set -e

APP_NAME="mino"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

echo "═══════════════════════════════════════════"
echo "  mino 打包工具"
echo "═══════════════════════════════════════════"

# 清理旧的构建产物
rm -rf build/bin build/staging

# 构建
export PATH=$HOME/go/bin:$PATH

PLATFORM="darwin/arm64"
ARCH_SUFFIX=""
if [ "$1" = "intel" ]; then
    PLATFORM="darwin/amd64"
    ARCH_SUFFIX="-intel"
    echo ">>> 构建 Intel (amd64) 版本..."
else
    echo ">>> 构建 Apple Silicon (arm64) 版本..."
fi

wails build -f -platform "$PLATFORM" -clean 2>&1 | grep -v "^$"

# 输出文件名
OUTPUT_DMG="${APP_NAME}${ARCH_SUFFIX}.dmg"
STAGING_DIR="build/staging"

# 准备 DMG 内容
echo ""
echo ">>> 准备 DMG 内容..."
mkdir -p "$STAGING_DIR"
cp -R "build/bin/${APP_NAME}.app" "$STAGING_DIR/"
ln -s /Applications "$STAGING_DIR/Applications"

# 生成 DMG
echo ">>> 生成 DMG..."
rm -f "$OUTPUT_DMG"

hdiutil create \
  -volname "$APP_NAME" \
  -srcfolder "$STAGING_DIR" \
  -ov -format UDZO \
  -imagekey zlib-level=9 \
  "$OUTPUT_DMG" 2>/dev/null

# 清理
rm -rf "$STAGING_DIR"

# 计算大小
DMG_SIZE=$(du -h "$OUTPUT_DMG" | cut -f1)

echo ""
echo "═══════════════════════════════════════════"
echo "  ✅ 打包完成"
echo "  📦 $(pwd)/${OUTPUT_DMG}"
echo "  📏 ${DMG_SIZE}"
echo "═══════════════════════════════════════════"
echo ""
echo "  分发给其他人后，对方需要："
echo "    1. 右键 → 打开（首次运行）"
echo "    2. 或 系统设置 → 隐私与安全性 → 仍然打开"
echo ""

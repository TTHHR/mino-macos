#!/bin/bash
# Start the app in development mode with hot-reload
export PATH=$HOME/go/bin:$PATH
export https_proxy=http://127.0.0.1:2080
export http_proxy=http://127.0.0.1:2080

cd "$(dirname "$0")"
wails dev

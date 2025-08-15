@echo off
REM 设置 protoc 路径
set PATH=%PATH%;D:\dev\protoc-win64\bin

REM 设置 Go bin 路径
set PATH=%PATH%;%USERPROFILE%\go\bin

REM 生成 protobuf 文件
protoc --go_out=. --go-vtproto_out=. --go-vtproto_opt=features=marshal+unmarshal+size *.proto

if %errorlevel% neq 0 (
    echo protobuf 文件生成失败
    pause
    exit /b %errorlevel%
)

echo protobuf 文件生成成功 success
pause
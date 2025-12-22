@echo off
REM 生成protobuf代码的脚本（Windows）

echo 生成Go protobuf代码...
protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative ..\proto\fastls.proto

echo 生成Python protobuf代码...
python -m grpc_tools.protoc -I..\proto --python_out=. --grpc_python_out=. ..\proto\fastls.proto

echo 代码生成完成！


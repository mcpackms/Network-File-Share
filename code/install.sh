#!/bin/bash
#请将编译完成的文件放在此文件下bin/nsf
[ "$(id -u)" -eq 0 ] || { echo >&2 "Requires root permission 需要 root！"; exit 1; }
echo "
  _   _   ____    _____
 | \ | | / ___|  |  ___|
 |  \| | \___ \  | |_
 | |\  |  ___) | |  _|
 |_| \_| |____/  |_|
"
read -p "Do you need to customize the installation? 需要自定义安装吗?  测试功能，不建议! (y/n) " answer

answer=$(echo "$answer" | tr '[:upper:]' '[:lower:]' | xargs)

case $answer in
    y|yes)
    read -p "Enter the installation path 请输入安装路径   " dir
        ;;
    n|no)
    dir=/usr
        ;;
    *)
        echo "Invalid input. Please enter y or n."
        exit 1
        ;;
esac
if [ ! -f "$PWD/bin/nsf" ]; then
    echo >&2 "找不到文件：$PWD/bin/nsf"
    echo >&2 "请检查路径"
    exit 1
fi
rm /bin/nfs
cp $PWD/bin/nsf $dir/nsf
ln -s $dir/nsf /bin/nsf
chmod +x /bin/nsf
echo The installation is complete 安装完成

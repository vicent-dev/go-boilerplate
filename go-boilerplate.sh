#!/bin/bash


read -p "Insert project name: " project_name

git clone git@github.com:vicent-dev/go-boilerplate.git ${project_name} &&

find ./${project_name} -type * -exec sed -i 's/go-boilerplate/${project_name}/g' {} +




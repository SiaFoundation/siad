#!/usr/bin/env bash
set -e

# config

changelog_md=../CHANGELOG.md
changelog_files_dir=../changelog
head_filename=changelog-head.md
tail_filename=changelog-tail.md
IFS=$'\n' # fix for loop filenames with spaces 

# functions

function add_items {
    items_header="$1"
    items_folder="$2"
    
    echo "  > writing $items_header"
    
    items_header_written=false
    items_list=$(find ./"$version" -wholename "*/$items_folder/*.md" | sort)
    for item in $items_list
    do
        if [ "$items_header_written" == false ]
    	then
    	    items_header_written=true
    		echo "    > writing $items_header header"
    		echo "**$items_header**" >> "$changelog_md"
    		echo "    > writing $items_header"
    	fi
    	echo "      > $item"
    	cat "$item" >> "$changelog_md"
    done
}


# get script location
pushd $(dirname "$0")

# work from "changelog" folder
pushd "$changelog_files_dir"

# Write the head of the changelog
echo 'writing head of changelog.md'
cp "$head_filename" "$changelog_md"


# versions, item headers, items

echo "getting versions in reverse order"
version_list=$(find * -maxdepth 1 -name "v*" | sort -r --version-sort)

echo found following versions:
echo --- begin ---
for version in $version_list
do
	echo "  $version"
done
echo ---  end  ---

echo writing versions to changelog...
for version in $version_list
do
    echo ">  writing version header: $version"
    echo "" >> "$changelog_md"
    echo "### $version" >> "$changelog_md"
    
    add_items "Key Updates" "key-updates"
    add_items "Bugs Fixed" "bugs-fixed"
    add_items "Other" "other"
done
echo writing versions to changelog: done



# tail

echo 'writing tail (end) of changelog.md'
tail_content=$(<"$tail_filename")
echo "" >> "$changelog_md"
echo "$tail_content" >> "$changelog_md"

popd
popd

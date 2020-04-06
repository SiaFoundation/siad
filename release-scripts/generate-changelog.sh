#!/usr/bin/env bash
set -e

# Generate CHANGELOG.md from changelog directory

# config

generate_till_version=v1.4.7
changelog_md=../CHANGELOG.md
changelog_files_dir=../changelog
head_filename=changelog-head.md
tail_filename=changelog-tail.md
IFS=$'\n' # fix for loop filenames with spaces 

######################################
# Add changelog item to changelog file
# Globals:
#   version
# Arguments:
#   item section header
#   item section folder
# Outputs:
#   Writes section header to changelog file
#   Writes item to changelog file
######################################
function add_items {
    items_header="$1"
    items_folder="$2"
    
    echo "  > writing $items_header"
    
    section_has_items=false
    items_list=$(find ./"$version" -wholename "*/$items_folder/*.md" | sort)
    new_line=false
    for item in $items_list
    do
        if [ "$section_has_items" == false ]
    	then
    	    section_has_items=true
    		echo "    > writing $items_header header"
    		echo "**$items_header**" >> "$changelog_md"
    		echo "    > writing $items_header"
    	fi
    	echo "      > $item"

        # remove trailing new lines from items
        # to fix markdown rendering
        text="$(printf "%s" "$(< $item)")"

        echo "$text" >> "$changelog_md"
    done

    # add new line to fix markdown rendering
    if [ "$section_has_items" == true ]
    then
        echo "" >> "$changelog_md"
    fi
}

# get script location
pushd $(dirname "$0")

# work from "changelog" folder
pushd "$changelog_files_dir"

# Write the head of the changelog
echo 'writing head of changelog.md'
cp "$head_filename" "$changelog_md"

# Get versions to be added to the changelog
echo "getting versions in reverse order"
version_list=$(find * -maxdepth 1 -name "v*" | sort -r --version-sort)

echo found following versions:
echo --- begin ---
for version in $version_list
do
	echo "  $version"
done
echo ---  end  ---

# Write versions and add changelog items to the changelog
echo writing versions to changelog...
upcoming_version_found=false
for version in $version_list
do
    versions_compare="$version
$generate_till_version"
    
    # check if current version should be included
    if [ "$versions_compare" == "$(sort --version-sort <<< "$versions_compare")" ]
    then
        echo "version $version WILL be included to changelog file"
        echo ">  writing version header: $version"
        echo "" >> "$changelog_md"
        
        # echo current month in English and year in format
        # '## Mar 2020:'
        echo "## $(LC_ALL=C date +%b) $(date +%Y):"
        
        echo "### $version" >> "$changelog_md"
        
        add_items "Key Updates" "key-updates"
        add_items "Bugs Fixed" "bugs-fixed"
        add_items "Other" "other"
    else
        echo "version $version WILL NOT be included to changelog file"
        upcoming_version_found=true
    fi
done
echo writing versions to changelog: done

# Generate upcoming version directory structure
if [ "$upcoming_version_found" == false ]
then
    # Calculate new version from current version
    upcoming_version=$(echo "$generate_till_version" | awk -F. -v OFS=. 'NF==1{print ++$NF}; NF>1{$NF=sprintf("%0*d", length($NF), ($NF+1)); print}')
    echo "generating directory structure for upcoming version $upcoming_version ..."

    for section in 'key-updates' 'bugs-fixed' 'other'
    do
        # create section directory
        mkdir -p "$upcoming_version/$section"

        # create dummy files for git commit to catch empty section directories
        touch "$upcoming_version/$section/.init"
    done
fi

# Write the tail of the changelog
echo 'writing tail of changelog.md'
tail_content=$(<"$tail_filename")
echo "" >> "$changelog_md"
echo "$tail_content" >> "$changelog_md"

popd
popd

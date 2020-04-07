#!/usr/bin/env bash
set -e

# Generate CHANGELOG.md from changelog directory

# config

generate_till_version=v1.4.7
changelog_md=../CHANGELOG.md
changelog_files_dir=../changelog
head_filename=changelog-head.md
mid_filename=changelog-mid.md
tail_filename=changelog-tail.md
IFS=$'\n' # fix for loop filenames with spaces
final_version=false # updates tail, removes latest version items

if [[ $1 == final ]]; then
    final_version=true
fi

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
    
    section_has_items=false
    items_list=$(find ./"$version" -wholename "*/$items_folder/*.md" | sort)
    new_line=false
    for item in $items_list
    do
        if [ "$section_has_items" == false ]
    	then
            # Write section header
    	    section_has_items=true
    		echo "**$items_header**" >> "$mid_filename"
    	fi
        # remove trailing new lines from items
        # to fix markdown rendering
        text="$(printf "%s" "$(< $item)")"

        echo "$text" >> "$mid_filename"
    done

    # add new line to fix markdown rendering
    if [ "$section_has_items" == true ]
    then
        echo "" >> "$mid_filename"
    fi
}

# get script location
pushd $(dirname "$0") > /dev/null

# work from "changelog" folder
pushd "$changelog_files_dir" > /dev/null

# Write the head of the changelog
echo 'writing head of changelog.md'
cp "$head_filename" "$changelog_md"

# Create temp changelog-mid.md
touch $mid_filename

# Get versions to be added to the changelog in reverse order
version_list=$(find * -maxdepth 1 -name "v*" | sort -r --version-sort)

# Write versions and add changelog items to the changelog
upcoming_version_found=false
for version in $version_list
do
    versions_compare="$version
$generate_till_version"
    
    # check if current version should be included
    if [ "$versions_compare" == "$(sort --version-sort <<< "$versions_compare")" ]
    then
        echo "writing version: $version"
        
        # echo current date month (in English), day and year in format
        # '## Mar 30, 2020:'
        echo "## $(LC_ALL=C date +%b) $(date +%d), $(date +%Y):" >> "$mid_filename"
        echo "### $version" >> "$mid_filename"
        
        add_items "Key Updates" "key-updates"
        add_items "Bugs Fixed" "bugs-fixed"
        add_items "Other" "other"

        if [ "$final_version" == "true" ]
        then
            rm -rf "$version"
        fi
    else
        echo "version $version WILL NOT be included to changelog file"
        upcoming_version_found=true
    fi
done

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

if [ "$final_version" == "true" ]
then
    echo "" >> "$changelog_md"

    # Save changelog mid to changelog tail

    # Append the tail of the changelog to the mid
    cat "$tail_filename" >> "$mid_filename"

    # Update tail with mid
    echo 'updating tail of changelog'
    mv "$mid_filename" "$tail_filename"
else
    echo "" >> "$changelog_md"

    # Write the mid of the changelog
    echo 'writing mid of changelog.md'
    cat "$mid_filename" >> "$changelog_md"
    rm "$mid_filename"
fi

# Write the tail of the changelog
echo 'writing tail of changelog.md'
cat "$tail_filename" >> "$changelog_md"

popd > /dev/null
popd > /dev/null

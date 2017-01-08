package hostinfo

import (
	"os"
	"path/filepath"
	"io/ioutil"
	
	"bufio"
	
	"strings"	
	"strconv"
)

// SysFS walker interface -- walks over sysfs filesystem entries

type sysfsWalker struct {
	// Root of the walking 
	root string
	
	// List of files, and current position + cached name  
	files []os.FileInfo
	index int
	name string
}

// Open sysfs path for walking
func sysfsOpen(root string) (walker *sysfsWalker, err error) {
	walker = &sysfsWalker{
		root: root,
		index: -1,
	}
	walker.files, err = ioutil.ReadDir(root) 
	
	if err != nil {
		return nil, err 
	}
	return walker, nil
}

// Open single subnode in a root node 
func sysfsOpenNode(root string, name string) (*sysfsWalker, error) {
	fi, err := os.Stat(filepath.Join(root, name))
	if err != nil {
		return nil, err 
	}
	
	return &sysfsWalker{
		root: root,
		index: 0,
		name: name, 
		files: []os.FileInfo{fi},
	}, nil
}

// Advances walker one step forward and returns true if there is 
// more entries 
func (walker *sysfsWalker) Next() bool {
	for {
		walker.index++
		if walker.index >= len(walker.files) {
			return false
		}
		
		walker.name = walker.files[walker.index].Name()
		if len(walker.name) >= 0 && walker.name[0] == '.' {
			continue
		}
		
		return true
	}
}

func (walker *sysfsWalker) GetName() string {
	return walker.name
}

func (walker *sysfsWalker) GetFileInfo() os.FileInfo {
	return walker.files[walker.index]
}

func (walker *sysfsWalker) openFile(path string) *os.File {
	// Absolute path for property file
	path = filepath.Join(walker.root, walker.name, path)
	file, err := os.Open(path)
	
	if err != nil {
		trace(HITraceHelpers, "Error opening file '%s': %v", path, err)
		return nil
	}
	
	return file
}

func (walker *sysfsWalker) ReadLine(path string) string {
	file := walker.openFile(path)
	if file == nil {
		return ""
	}
	defer file.Close()
	
	buf := bufio.NewReader(file)
	line, err := buf.ReadString('\n')
	if err != nil {
		trace(HITraceHelpers, "Error reading subfile '%s': %v", path, err)
		return ""
	}
	
	return strings.TrimSuffix(line, "\n")
}

func (walker *sysfsWalker) ReadInt(path string, base, bitSize int, 
					  	           defVal int64) int64 {
	line := walker.ReadLine(path)
	if len(line) == 0 {
		return defVal
	}
	
	value, err := strconv.ParseInt(line, base, bitSize)
	if err != nil {
		trace(HITraceHelpers, "Error parsing integer '%s' (%s): %v", path, line, err)
		return defVal
	}
	return value
}

func (walker *sysfsWalker) ReadLink(path string) string {
	path = filepath.Join(walker.root, walker.name, path)
	link, err := os.Readlink(path) 
	if err != nil {
		trace(HITraceHelpers, "Error reading link '%s': %v", path, err)
		return ""
	}
	
	return link
}

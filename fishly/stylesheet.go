package fishly 

import (
	"strings"
	
	"github.com/go-ini/ini"	
)

type StyleSheetNode interface {
	// Children of this stylesheet node are embedded stylesheets nodes. Has 
	// special node: ellipsis "..." which can match many path parts
	// Other nodes are exact names. This interface is only factory 
	// for creating nodes and parsing them from ini
	
	// Returns existing child
	GetChild(name string) StyleSheetNode
	
	// Creates child
	CreateChild(name string) StyleSheetNode
	
	// Returns style object (it will be filled in by ini.Section)
	CreateStyle() interface{}
}

// StyleSheet path keeps track of style sheets in a way similar
// to token path 
type StyleSheetPath struct {
	Path []string
}

type StyleSheetIterator interface {
	// Returns root or tail node
	GetCurrent() StyleSheetNode
	
	// Tries to enter node with the specified name
	Enter(path string)
	
	// Goes back up to index "index"
	Back(index int)
}

func LoadStyleSheet(files []string, rootNode StyleSheetNode) (error) {
	styleFiles := make([]interface{}, len(files))
	for index, styleFile := range files {
		styleFiles[index] = styleFile
	}
	
	styleMap, err := ini.Load(styleFiles[0], styleFiles[1:]...)
	if err != nil {
		return err
	}
	
	for _, section := range styleMap.Sections() {
		// Iteratively create node corresponding to this section's path
		sspath := new(StyleSheetPath)
		sspath.Parse(section.Name())
		
		node := rootNode
		for _, component := range sspath.Path {
			newNode := node.GetChild(component)
			if newNode == nil {
				newNode = node.CreateChild(component)
			}
			node = newNode 
		}
		
		section.MapTo(node.CreateStyle())
		
		// Create empty subnodes with empty style
		if section.HasKey("SubNodes") {
			for _, subName := range section.Key("SubNodes").Strings(",") {
				subName = strings.TrimSpace(subName)
				subnode := node.CreateChild(subName)
				subnode.CreateStyle()
			}
		}
		
		// XXX: We use '.' to build up stylesheet sections so as go-ini for 
		// parent/child sections. Since parents go prior children, we delete
		// them so child section wouldn't copy parent key 
		styleMap.DeleteSection(section.Name())
	}
	
	return nil
}

// Parses path and returns its components
func (sspath *StyleSheetPath) Parse(path string) {
	segments := strings.Split(path, "...")
	sspath.Path = make([]string, 0)
	
	for index, segment := range segments {
		sspath.Path = append(sspath.Path, strings.Split(segment, ".")...)
		
		if index < (len(segments) - 1) {
			sspath.Path = append(sspath.Path, "...")
		}
	}
}

// Takes token path, adapts it to stylesheet node while taking in consideration
// internal structure of stylesheet. Optimizes whole process a bit as iter
// caches entered nodes in the same manner sspath keeps them (note: this will
// require equality of paths inside iter). Return true if they found fully
// matching node in iter
func (sspath *StyleSheetPath) Update(iter StyleSheetIterator, path *TokenPath) bool {
	tokenIndex := 0
	
	compareLoop:
	for styleIndex, component := range sspath.Path {
		if tokenIndex >= len(path.path) {
			iter.Back(styleIndex)
			sspath.Path = sspath.Path[:styleIndex]	
			break
		}
		
		if component == "..." {
			// Move forward until we find a matching entry
			next := sspath.Path[styleIndex + 1]
			for fwdIndex := tokenIndex ; fwdIndex < len(path.path) ; fwdIndex++ {
				if path.path[fwdIndex] == next {
					tokenIndex = fwdIndex
					continue compareLoop
				}
			}
		} else if component == path.path[tokenIndex] {
			// Direct match
			tokenIndex++
			continue
		} 
		
		// No more matching tokens in sspath, go back
		iter.Back(styleIndex)
		sspath.Path = sspath.Path[:styleIndex]
		break
	}
	
	// Now try to build up remaining tokens
	node := iter.GetCurrent()
	var ellipsis StyleSheetNode 
	for ; tokenIndex < len(path.path) ; tokenIndex++ {
		token := path.path[tokenIndex]
		
		// We didn't yet give a try with ellipsis, so try direct match
		var child StyleSheetNode
		if ellipsis == nil {
			child = node.GetChild(token)
			if child == nil {
				ellipsis = node.GetChild("...")
				
				// Dead end, current node doesn't have a child
				// and does not have ellipsis node
				if ellipsis == nil {
					return false
				}
			}
		} else {
			child = ellipsis.GetChild(token)
		}
		
		// Found a child, update the paths
		if child != nil {
			if ellipsis != nil {
				sspath.Path = append(sspath.Path, "...")
				iter.Enter("...") 
				ellipsis = nil
			}
			
			sspath.Path = append(sspath.Path, token)
			iter.Enter(token)
			node = child
		}
	}
	
	// If we didn't try ellipsis at last step, we got an actual node
	// if it doesn't contain style, default-initialize it
	return (ellipsis == nil)
}

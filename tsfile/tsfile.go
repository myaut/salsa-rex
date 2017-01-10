package tsfile

import (
	"fmt"
	
	"io"
	"bytes"
	
	"sync"	
	"sync/atomic"
	
	"time"
	
	"encoding/binary"
)

// TSFile -- a fast (?) implementation of a sequental files to keep events 
// in binary format. The idea is to get as small as possible write operations
// and bytes per each event entry to avoid "dirty" results from tracing own writes.
//
// Based on TSF format from TSLoad, thus this implementation extends its format
// by splitting it into individual extents.
//
// Format for V1:					for V2:						
//
// +--+-------+---+---    ---+		+--+-------+------+------     ---+--+-------+--
// |  |     ##|   |    ...   |		|  |     ##|      |   	  ...    |  |     ##| 
// +--+-------+---+---    ---+		+--+-------+------+------  	  ---+--+-------+--
//	 ^     ^    |           |		 ^  ^   ^	 ^      			  ^   ^
//   |     |    <- entries ->		 |  |   |	 <- pages 0 .. 240 -> |   |
//   |	   +---- TSFSchemaHeader	 |  + TSFPageHeader[0:240]        |   + [241:...
//	 +-  TSFHeader					 + TSFHeader #1					  + TSFHeader #2
//                                  <----------- extent ------------> 
//
// In V2 format each page may contain schema, header or multiple entries  
//


const (
	pageSize = 4096
	
	tsFileMagic = "TSFILE"
	tsFileMagicLength = 6
	
	fieldNameLength = 32
	schemaNameLength = 32
	maxFieldCount = 64
	
	initialPageCount = 4
	
	superBlockCount = 4
	
	hdrByteCount = 72
	tagsPerHeader = 240
	
	schemaSize = 3600 + schemaNameLength
)

type TSFSuperBlock struct {
	// Time stamp of writing this super block in nanoseconds
	Time uint64
	
	// Total number of entries (V1) or pages (V2)
	Count uint32
	
	Pad uint32
}

type TSFHeader struct {
	Magic [tsFileMagicLength]byte
	Version uint16
	
	SuperBlocks [superBlockCount]TSFSuperBlock
	
	// Header is either followed by schema (V1) or page headers (V2)
}

const (
	// Flag meaning that this page contains schema, not the actual data
	TSFSchemaPage = 1 << iota
)

const (
	// A header tag
	TSFPgHeader = -1 
)

type TSFPageHeader struct {
	// Tag of the page. Pages which contain same structures (i.e. events
	// will have same tags), altough some of them will have special flags
	// Tag should match index in schemas array
	Tag int16
	Flags uint16
	
	// Number of entries in this page
	Count uint32
	Pad2 uint64
}

type tsfPage struct {
	mu sync.Mutex
	buf *bytes.Buffer
	
	// Size of the page
	size uint32
	
	// Is this page full
	full bool
}

type TSFile struct {
	header TSFHeader
	pageHeaders []TSFPageHeader
	schemas []TSFSchemaHeader
	
	// Only single thread can seek and perform I/O at a moment
	mu sync.RWMutex
	file io.ReadWriteSeeker
	
	// Accessible version of ts file
	version int
	
	// Index of page which contains actual header
	headerPageId uint32
	
	// Index of super block in actual header
	sbIndex uint32
	
	// Default page size for data  
	pageSize uint32
	
	// Number of pages that are filled up
	fullPages uint32
	
	schemaCount uint32
	entryCount uint32
	pageCount uint32
	
	dataPagesCache map[int][]uint32
	pageCache map[uint32]*tsfPage
}

// Creates new TSFile: initializes header
func NewTSFile(file io.ReadWriteSeeker, version int) (*TSFile, error) {
	if version < 1 || version > 2 {
		return nil, fmt.Errorf("Unsupported TSFile version %d", version)
	}
	
	tsf := new(TSFile)
	tsf.file = file
	tsf.version = version
	tsf.pageCache = make(map[uint32]*tsfPage)
	tsf.dataPagesCache = make(map[int][]uint32)
	tsf.pageSize = pageSize
	
	// For version 1 -- explicitly allocate header page (v2 will 
	// lazily allocate it in first addPage)
	if version == 1 {
		tsf.allocateDataPage(TSFPgHeader, 0)
	}
	
	// Initialize header basic fields
	copy(tsf.header.Magic[:], []byte(tsFileMagic))
	tsf.header.Version = uint16(version)
	
	return tsf, nil
}

// Closes TSFile and writes pages (doesn't close undelying file)
func (tsf *TSFile) Close() error {
	return tsf.writePages(true)
}

// Adds new schema to a file and returns allocated tag
func (tsf *TSFile) AddSchema(header *TSFSchemaHeader) (int, error) {
	schemaId := int(atomic.AddUint32(&tsf.schemaCount, 1) - 1)
	entrySize := uint32(header.EntrySize)
	
	switch tsf.version {
		case 1:
			if schemaId > 0 {
				return -1, fmt.Errorf("Cannot add more than one schema to TSFv1")
			}
			// In v1 -- align page size by object size
			tsf.pageSize = (pageSize + entrySize - 1) / entrySize * entrySize
		case 2:
			if tsf.pageSize < entrySize {
				return -1, fmt.Errorf("Entry is too big for %d bytes pages", tsf.pageSize)
			}
		
			// Bonus -- allocate a page to keep schema 
			page, _ := tsf.allocateDataPage(schemaId, TSFSchemaPage)
			binary.Write(page.buf, binary.LittleEndian, header)
			page.full = true
	}
	
	tsf.mu.Lock()
	defer tsf.mu.Unlock()
	
	// We want to get schemaId early (for V2 allocatePage), but if AddSchema 
	// is called concurrently, we may append into wrong index
	if len(tsf.schemas) > schemaId {
		tsf.schemas[schemaId] = *header	
	} else {
		for len(tsf.schemas) <= schemaId {
			tsf.schemas = append(tsf.schemas, *header)
		}
	}
	return schemaId, nil
}

// Adds entries to the file (write is deferred)
func (tsf *TSFile) AddEntries(schemaId int, entries []interface{}) error {	
	if schemaId < 0 || uint32(schemaId) >= tsf.schemaCount {
		return fmt.Errorf("Undefined schema #%d", schemaId)
	}
	
	start := 0
	entrySize := tsf.getEntrySize(schemaId)  
	for start < len(entries) {
		page, pageId := tsf.getDataPage(schemaId)
		if page == nil {
			page, pageId = tsf.allocateDataPage(schemaId, 0)
		} 
		
		count, err := page.writeEntries(start, entries, entrySize)
		if err != nil {
			return err
		}
		
		tsf.accountPageEntries(uint32(count), pageId, page.full)
		start += count
	}
	
	return tsf.writePages(false)
}

func (tsf *TSFile) getEntrySize(schemaId int) uint32 {
	tsf.mu.RLock()
	defer tsf.mu.RUnlock()
	
	return uint32(tsf.schemas[schemaId].EntrySize)
}

// Tries to get actual data page for writing
func (tsf *TSFile) getDataPage(schemaId int) (*tsfPage, uint32) { 
	tsf.mu.RLock()
	defer tsf.mu.RUnlock()
	
	if pages, ok := tsf.dataPagesCache[schemaId]; ok {
		for _, pageId := range pages {
			if page, ok := tsf.pageCache[pageId]; ok {
				if !page.full {
					return page, pageId
				}
			}
		}
	}
	
	return nil, 0
}

func (tsf *TSFile) accountPageEntries(count uint32, pageId uint32, full bool) {
	tsf.mu.RLock()
	defer tsf.mu.RUnlock()
	
	atomic.AddUint32(&tsf.pageHeaders[pageId].Count, count)
	atomic.AddUint32(&tsf.entryCount, count)	
	if full {
		atomic.AddUint32(&tsf.fullPages, 1)
	}
}

func (page *tsfPage) writeEntries(start int, entries []interface{}, entrySize uint32) (int, error) {
	// Write to page until it would be full
	page.mu.Lock()
	defer page.mu.Unlock()
	
	buf := page.buf
	count := 0
	for (start + count) < len(entries) {
		if (uint32(buf.Len()) + entrySize) > page.size {
			// No more space for entries in this page (and this page
			// is eligible for commiting)
			page.full = true
			break
		}
		
		size := buf.Len()
		err := binary.Write(buf, binary.LittleEndian, entries[start+count])
		n := buf.Len() - size
		
		if err != nil {
			return count, err
		}
		if uint32(n) != entrySize {
			buf.Truncate(size)
			return count, fmt.Errorf("Invalid entry of size %d, %d is expected", n, entrySize)
		}
		
		count++
	}
	
	return count, nil
}

// Writes and evicts full pages (or if sync is set, all pages), updates
// header and writes it too
func (tsf *TSFile) writePages(sync bool) error {
	if atomic.SwapUint32(&tsf.fullPages, 0) == 0 && !sync {
		// There is no full data pages at the moment (or concurrent writer
		// is running)
		return nil
	}
	
	tsf.updateHeader(atomic.LoadUint32(&tsf.headerPageId))
	
	tsf.mu.Lock()
	defer tsf.mu.Unlock()
	
	for pageId, page := range tsf.pageCache {
		if page.full || sync {
			err := tsf.writePage(page, pageId)
			if err != nil {
				return err
			}
			
			// Evict full data pages
			if pageId != tsf.headerPageId {
				delete(tsf.pageCache, pageId)
				
				pageTag := int(tsf.pageHeaders[pageId].Tag)
				if dataPages, ok := tsf.dataPagesCache[pageTag]; ok {
					for index, dataPageId := range dataPages {
						if dataPageId == pageId {
							tsf.dataPagesCache[pageTag] = append(dataPages[:index], 
									dataPages[index+1:]...)
							break
						}
					}
				} 
			}
		}
	}
	
	return nil
}

// Write page (call with tsf.mu locked)
func (tsf *TSFile) writePage(page *tsfPage, pageId uint32) error {
	// Should be called for full page, header page or during close
	// so we shouldn't lock page.mu here as nobody except us should write
	// into page buffer
	
	// Number of headers going prior to us
	numHeaders := (pageId + tagsPerHeader - 1) / tagsPerHeader
	offset := (pageId - numHeaders) * tsf.pageSize + numHeaders * pageSize
	
	_, err := tsf.file.Seek(int64(offset), io.SeekStart)
	if err != nil {
		return err
	}
	
	_, err = tsf.file.Write(page.buf.Bytes())
	return err
}

// Rewrites header page and marks it as full
func (tsf *TSFile) updateHeader(oldHeaderPageId uint32) *tsfPage {
	// Update super block (up to 4 can do it concurrently)
	sbIndex := atomic.AddUint32(&tsf.sbIndex, 1)
	sb := &tsf.header.SuperBlocks[sbIndex % superBlockCount]
	sb.Time = uint64(time.Now().UnixNano())
	switch tsf.version {
		case 1:
			sb.Count = atomic.LoadUint32(&tsf.entryCount)
		case 2:
			sb.Count = atomic.LoadUint32(&tsf.pageCount)
	}
	
	tsf.mu.Lock()
	defer tsf.mu.Unlock()
	
	if page, ok := tsf.pageCache[oldHeaderPageId]; ok {
		buf := page.buf
		buf.Truncate(0)
		
		binary.Write(buf, binary.LittleEndian, tsf.header)
		switch tsf.version {
			case 1:
				binary.Write(buf, binary.LittleEndian, tsf.schemas[0])
			case 2:
				// Select range of page headers corresponding to this header
				headerIdx := oldHeaderPageId / tagsPerHeader
				
				start, end := int(headerIdx * tagsPerHeader), int((headerIdx+1) * tagsPerHeader)				
				if end >= len(tsf.pageHeaders) {
					end = len(tsf.pageHeaders)
				}
				
				binary.Write(buf, binary.LittleEndian, tsf.pageHeaders[start:end])
		}
		
		page.full = true
		return page
	}
	
	return nil
}

// Allocates new page: inserts page header to and page object and returns
// page along with its index
func (tsf *TSFile) allocateDataPage(tag int, flags uint) (*tsfPage, uint32) {
	page := tsf.newPage(tsf.pageSize)
	
	pageId := atomic.AddUint32(&tsf.pageCount, 1) - 1
	if tsf.version >= 2 && (pageId == atomic.LoadUint32(&tsf.headerPageId) * tagsPerHeader) {
		// The should be a header page, so we can start new extent
		tsf.insertPage(tsf.newPage(pageSize), pageId, TSFPgHeader, 0)
		oldHeaderPageId := atomic.SwapUint32(&tsf.headerPageId, pageId)
		
		pageId = atomic.AddUint32(&tsf.pageCount, 1) - 1
		
		tsf.updateHeader(oldHeaderPageId)
	}
	
	return tsf.insertPage(page, pageId, tag, flags)
}

func (tsf *TSFile) newPage(size uint32) *tsfPage {	
	page := new(tsfPage)
	page.buf = bytes.NewBuffer([]byte{})
	page.size = size
	page.buf.Grow(int(size))
	
	return page
}

func (tsf *TSFile) insertPage(page *tsfPage, pageId uint32, tag int, flags uint) (*tsfPage, uint32) {
	// Insert page to a list of pages and update header
	tsf.mu.Lock()
	defer tsf.mu.Unlock()
	
	for int(pageId) >= len(tsf.pageHeaders) {
		tsf.pageHeaders = append(tsf.pageHeaders, 
				make([]TSFPageHeader, initialPageCount)...)
	}
	
	tsf.pageHeaders[pageId].Tag = int16(tag)
	tsf.pageHeaders[pageId].Flags = uint16(flags) 
	tsf.pageCache[pageId] = page
	
	if tag >= 0 && flags == 0 {
		// If we accidentally created copy of data page, it is not bad,
		// we're simply save second page for convenience and take first
		pages := tsf.dataPagesCache[tag]
		
		if len(pages) == 0 {
			tsf.dataPagesCache[tag] = []uint32{pageId}
		} else {
			tsf.dataPagesCache[tag] = append(pages, pageId)
			return tsf.pageCache[pages[0]], pages[0] 
		}
	}
	
	return page, pageId
}

// A helper to decode zero-terminated string
func DecodeCStr(cStr []byte) string {
	l := len(cStr)
	for i := 0; i < len(cStr); i++ {
        if cStr[i] == 0 {
        	l = i
            break
        }
    }
	
	return string(cStr[:l])
}

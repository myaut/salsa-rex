package tsfile

import (
	"fmt"

	"bytes"
	"io"

	"sync"
	"sync/atomic"

	"time"

	"strconv"

	"encoding/binary"
	"reflect"
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

	tsFileMagic       = "TSFILE"
	tsFileMagicLength = 6

	fieldNameLength  = 32
	schemaNameLength = 32
	maxFieldCount    = 64

	initialPageCount = 4

	superBlockCount = 4

	hdrByteCount  = 72
	tagsPerHeader = 240

	maxCachedPagesHigh = 24
	maxCachedPagesLow  = 16

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

	// Was version, now a bitmask of format flags
	FormatFlags uint16

	SuperBlocks [superBlockCount]TSFSuperBlock

	// Header is either followed by schema (V1) or page headers (V2)
}

type TSFPageTag int
type TSFSchemaId uint32
type TSFPageId uint32
type TSFFormatFlags uint16

const (
	// TSfile format flags. V1Ext is the extensions to format (such as time type)
	// not supported by TSLoad 1.0, but implemented in 1.1
	TSFFormatV1  TSFFormatFlags = 0x1
	TSFFormatV2  TSFFormatFlags = 0x2
	TSFFormatExt TSFFormatFlags = 0x10

	tsFileFormatVersionFlags   TSFFormatFlags = (TSFFormatV1 | TSFFormatV2)
	tsFileSupportedFormatFlags TSFFormatFlags = (tsFileFormatVersionFlags |
		TSFFormatExt)
)

const (
	// Flag meaning that this page contains schema, not the actual data
	TSFSchemaPage = 1 << iota
)

const (
	// Special tags

	// Empty page
	TSFTagEmpty TSFPageTag = 0

	// A header tag
	TSFTagHeader TSFPageTag = 1

	// First data page tag
	TSFTagData TSFPageTag = 32
)

type TSFPageHeader struct {
	// Tag of the page. Pages which contain same structures (i.e. events
	// will have same tags), altough some of them will have special flags
	// Tag should match index in schemas array
	Tag   uint16
	Flags uint16

	// Number of entries in this page
	Count uint32
	Pad   uint64
}

type tsfPage struct {
	mu  sync.Mutex
	buf *bytes.Buffer

	// number of data entries in this page
	count uint32

	// Size of the page
	size uint32

	// generation of page -- older pages are getting evicted
	generation uint32

	// Is this page was open for writing
	dirty bool

	// Does this page filled up with entries?
	full bool
}

type tsfPageIndex struct {
	// Page index
	pageId TSFPageId

	// Starting entry index
	start uint32
}

type tsfSchema struct {
	header TSFSchemaHeader

	// tag used by schema
	tag TSFPageTag

	// total number of entries of this kind
	count uint32

	// index of page ids per starting index
	pageIndex []tsfPageIndex
}

type TSFSeriesStats struct {
	// Corresponding page tag
	Tag TSFPageTag `json:"tag"`
	// Name of the schema
	Name string `json:"name"`
	// Number of entries for this series
	Count uint `json:"count"`
}

type TSFileStats struct {
	Series []TSFSeriesStats `json:"series"`
}

type TSFileStorage interface {
	io.ReadWriteSeeker
	io.Closer
}

type TSFile struct {
	header      TSFHeader
	pageHeaders []TSFPageHeader
	schemas     []tsfSchema

	// Only single thread can seek and perform I/O at a moment
	mu   sync.RWMutex
	file TSFileStorage

	// Number of entities holding reference to this file
	refCount int32

	// Accessible version of ts file anf version-specific values
	formatFlags TSFFormatFlags

	// Default page size for data
	pageSize uint32

	// Index of page which contains actual header
	headerPageId TSFPageId

	// Index of super block in actual header
	sbIndex uint32

	// Number of pages that are filled up
	fullPages uint32

	schemaCount uint32
	pageCount   TSFPageId

	// Current generation of pages
	pageGeneration uint32

	dataPagesCache map[TSFPageTag][]TSFPageId
	pageCache      map[TSFPageId]*tsfPage
}

func newTSFile(file TSFileStorage) *TSFile {
	tsf := new(TSFile)
	tsf.file = file
	tsf.pageCache = make(map[TSFPageId]*tsfPage)
	tsf.dataPagesCache = make(map[TSFPageTag][]TSFPageId)
	tsf.pageSize = pageSize
	tsf.refCount = int32(1)

	return tsf
}

// Creates new TSFile object for writing
func NewTSFile(file TSFileStorage, formatFlags TSFFormatFlags) (*TSFile, error) {
	if (formatFlags & ^tsFileSupportedFormatFlags) != 0 ||
		(formatFlags&tsFileFormatVersionFlags) == 0 {

		return nil, fmt.Errorf("Unsupported TSFile format flags %x", formatFlags)
	}

	tsf := newTSFile(file)
	tsf.formatFlags = TSFFormatFlags(formatFlags)

	// Initialize header basic fields
	copy(tsf.header.Magic[:], []byte(tsFileMagic))
	tsf.header.FormatFlags = uint16(formatFlags)

	// Allocate first (#0) header page
	tsf.allocateDataPage(TSFTagHeader, 0)

	return tsf, nil
}

// Loads existing TS file
func LoadTSFile(file TSFileStorage) (*TSFile, error) {
	tsf := newTSFile(file)

	// Read first header
	hdrPage, err := tsf.loadHeader(0)
	if err != nil {
		return nil, fmt.Errorf("Error reading first header: %v", err)
	}

	switch tsf.formatFlags.getVersion() {
	case TSFFormatV1:
		err = tsf.loadFileV1(hdrPage)
	case TSFFormatV2:
		err = tsf.loadFileV2(hdrPage)
	default:
		return nil, fmt.Errorf("Unsupported TSFile format flags %x", tsf.formatFlags)
	}

	if err != nil {
		return nil, err
	}
	return tsf, nil
}

// Functions for convert primitive values
func (pageHdr TSFPageHeader) getTag() TSFPageTag {
	return TSFPageTag(pageHdr.Tag)
}
func (pageTag TSFPageTag) toSchemaId() TSFSchemaId {
	return TSFSchemaId(pageTag - TSFTagData)
}
func (schemaId TSFSchemaId) toTag() TSFPageTag {
	return TSFPageTag(schemaId) + TSFTagData
}
func (pageTag TSFPageTag) isDataTag() bool {
	return pageTag >= TSFTagData
}
func (tsf *TSFile) isValidSchemaId(schemaId TSFSchemaId) bool {
	return int(schemaId) < len(tsf.schemas)
}
func nextPageId(ptr *TSFPageId) TSFPageId {
	return TSFPageId(atomic.AddUint32((*uint32)(ptr), 1) - 1)
}
func loadPageId(ptr *TSFPageId) TSFPageId {
	return TSFPageId(atomic.LoadUint32((*uint32)(ptr)))
}
func swapPageId(ptr *TSFPageId, pageId TSFPageId) TSFPageId {
	return TSFPageId(atomic.SwapUint32((*uint32)(ptr), uint32(pageId)))
}
func (flags TSFFormatFlags) hasFlag(flag TSFFormatFlags) bool {
	return (flags & flag) != 0
}
func (flags TSFFormatFlags) getVersion() TSFFormatFlags {
	return flags & tsFileFormatVersionFlags
}

func (tsf *TSFile) loadFileV1(hdrPage *tsfPage) error {
	sb := tsf.header.findSuperBlock()
	if sb == nil {
		return fmt.Errorf("Cannot find valid superblock in header")
	}

	// There is a single schema in v1, so load it and set entry count
	var schemaHdr TSFSchemaHeader
	hdrPage.read(&schemaHdr, hdrByteCount)
	tag, err := tsf.AddSchema(&schemaHdr)
	if err != nil {
		return err
	}
	tsf.schemas[tag.toSchemaId()].count = uint32(sb.Count)

	// generate page headers based on entry count in superblock
	entriesPerPage := tsf.pageSize / uint32(schemaHdr.EntrySize)
	entryCount := uint32(sb.Count)

	tsf.pageHeaders = append(tsf.pageHeaders, TSFPageHeader{Tag: uint16(TSFTagHeader)})
	pageHeader := TSFPageHeader{
		Tag:   uint16(tag),
		Count: entriesPerPage,
	}
	for ; entryCount > entriesPerPage; entryCount -= entriesPerPage {
		tsf.pageHeaders = append(tsf.pageHeaders, pageHeader)
	}
	if entryCount > 0 {
		pageHeader.Count = entryCount
		tsf.pageHeaders = append(tsf.pageHeaders, pageHeader)

		// If last page is not full page, use it for appending entries
		tsf.dataPagesCache[tag] = []TSFPageId{TSFPageId(len(tsf.pageHeaders) - 1)}
	}
	tsf.pageCount = TSFPageId(len(tsf.pageHeaders))

	return nil
}

func (tsf *TSFile) loadFileV2(hdrPage *tsfPage) error {
	// Load all page headers and headers first...
	haveHeader := true
	for haveHeader {
		sb := tsf.header.findSuperBlock()
		if sb == nil {
			return fmt.Errorf("Cannot find valid superblock in header #%d", tsf.headerPageId)
		}

		pageCount := TSFPageId(sb.Count)
		if pageCount < tsf.pageCount {
			return fmt.Errorf("Unexpected count of pages %d in newer sb in header #%d",
				sb.Count, tsf.headerPageId)
		}

		// sb.Count contains total number of pages, but tsf.pageCount has number
		// of pages in headers above, so difference between two values is
		// number of pages in this header
		oldPageCount := swapPageId(&tsf.pageCount, pageCount)
		hdrPageCount := (pageCount - oldPageCount)

		pageHeaders := make([]TSFPageHeader, hdrPageCount)
		err := hdrPage.read(pageHeaders, hdrByteCount)
		if err != nil {
			return fmt.Errorf("Error reading %d page headers from header #%d: %v",
				hdrPageCount, tsf.headerPageId, err)
		}
		tsf.pageHeaders = append(tsf.pageHeaders, pageHeaders...)

		// analyze headers -- read schemas and following headers
		haveHeader = false
		for relId, hdr := range pageHeaders {
			pageId := oldPageCount + TSFPageId(relId)
			pageTag := TSFPageTag(hdr.Tag)

			if pageTag == TSFTagHeader {
				hdrPage, err = tsf.loadHeader(pageId)
				if err != nil {
					return fmt.Errorf("Error loading header from page #%d: %v", pageId, err)
				}

				haveHeader = true

				// NOTE: at this point sb gets invalidated as we re-creating header
				// structure. That is why we cached pageCount above
			} else if (hdr.Flags & TSFSchemaPage) != 0 {
				err := tsf.loadSchemaV2(pageId, hdr)
				if err != nil {
					return fmt.Errorf("Error reading schema page #%d: %v", pageId, err)
				}
			} else if hdr.Flags == 0 && pageTag.isDataTag() {
				// Data page, account number of entries from this page
				err := tsf.loadDataTagV2(pageId, hdr)
				if err != nil {
					return fmt.Errorf("Error reading data tag #%d: %v", pageId, err)
				}
			}
		}
	}

	return nil
}

func (tsf *TSFile) loadSchemaV2(pageId TSFPageId, hdr TSFPageHeader) error {
	var schema TSFSchemaHeader
	schemaPage, err := tsf.readPage(pageId)
	if err != nil {
		return err
	}

	err = schemaPage.read(&schema, 0)
	if err != nil {
		return err
	}

	err = schema.Check(tsf.formatFlags.hasFlag(TSFFormatExt))
	if err != nil {
		return err
	}

	tsf.insertSchema(hdr.getTag().toSchemaId(), &schema)
	return nil
}

func (tsf *TSFile) loadHeader(pageId TSFPageId) (*tsfPage, error) {
	var header TSFHeader

	page, err := tsf.readPage(pageId)
	if err != nil {
		return nil, err
	}
	page.read(&header, 0)

	magic := DecodeCStr(header.Magic[:])
	if magic != tsFileMagic {
		return nil, fmt.Errorf("invalid magic: %v", strconv.Quote(magic))
	}

	if tsf.formatFlags == 0 {
		tsf.formatFlags = TSFFormatFlags(header.FormatFlags)
	} else if tsf.formatFlags != TSFFormatFlags(header.FormatFlags) {
		return nil, fmt.Errorf("unsupported format flags %x", header.FormatFlags)
	}

	tsf.headerPageId = pageId
	tsf.header = header
	return page, nil
}

func (tsf *TSFile) loadDataTagV2(pageId TSFPageId, hdr TSFPageHeader) error {
	schemaId := hdr.getTag().toSchemaId()
	if !tsf.isValidSchemaId(schemaId) {
		return fmt.Errorf("data page tag %d doesn't have corresponding schema", schemaId)
	}

	schema := &tsf.schemas[schemaId]
	if hdr.Count > 0 {
		schema.pageIndex = append(schema.pageIndex, tsfPageIndex{
			pageId: pageId,
			start:  schema.count,
		})
		schema.count += hdr.Count
	}

	return nil
}

// Finds valid superblock or returns nil if it doesn't exist
func (header *TSFHeader) findSuperBlock() *TSFSuperBlock {
	var lastSb *TSFSuperBlock

	for sbIndex := 0; sbIndex < superBlockCount; sbIndex++ {
		sb := &header.SuperBlocks[sbIndex]
		if sb.Time == 0 {
			continue
		}
		if lastSb == nil || lastSb.Time <= sb.Time {
			lastSb = sb
		}
	}

	return lastSb
}

// Gets TSFile reference or returns nil if file was already closed
func (tsf *TSFile) Get() *TSFile {
	if atomic.AddInt32(&tsf.refCount, 1) == 1 {
		atomic.StoreInt32(&tsf.refCount, 0)
		return nil
	}
	return tsf
}

// Detach current reference of TSFile from underlying storage and if it is
// last reference, return underlying storage object
func (tsf *TSFile) Detach() (TSFileStorage, error) {
	err := tsf.writePages(true)
	if atomic.AddInt32(&tsf.refCount, -1) <= 0 {
		return tsf.file, err
	}

	return nil, err
}

// Puts TSFile reference, writes pages and potentially closes underlying file
func (tsf *TSFile) Put() error {
	f, err := tsf.Detach()

	if f != nil {
		err2 := f.Close()
		if err == nil {
			return err2
		}
	}
	return err
}

// Adds new schema to a file and returns allocated tag
func (tsf *TSFile) AddSchema(header *TSFSchemaHeader) (TSFPageTag, error) {
	err := header.Check(tsf.formatFlags.hasFlag(TSFFormatExt))
	if err != nil {
		return -1, err
	}

	schemaId := TSFSchemaId(atomic.AddUint32(&tsf.schemaCount, 1) - 1)
	entrySize := uint32(header.EntrySize)
	schema := tsf.insertSchema(schemaId, header)

	switch tsf.formatFlags.getVersion() {
	case TSFFormatV1:
		if schemaId > 0 {
			return -1, fmt.Errorf("Cannot add more than one schema to TSFv1")
		}
		// In v1 -- align page size by object size
		tsf.pageSize = (pageSize + entrySize - 1) / entrySize * entrySize
	case TSFFormatV2:
		if tsf.pageSize < entrySize {
			return -1, fmt.Errorf("Entry is too big for %d bytes pages", tsf.pageSize)
		}

		// Bonus -- allocate a page to keep schema
		page, _ := tsf.allocateDataPage(schema.tag, TSFSchemaPage)
		binary.Write(page.buf, binary.LittleEndian, header)
		page.full = true
		page.dirty = true
	}

	return schema.tag, nil
}

func (tsf *TSFile) insertSchema(schemaId TSFSchemaId, header *TSFSchemaHeader) *tsfSchema {
	tsf.mu.Lock()
	defer tsf.mu.Unlock()

	schema := tsfSchema{
		header:    *header,
		tag:       schemaId.toTag(),
		pageIndex: make([]tsfPageIndex, 0),
	}

	// We want to get schemaId early (for V2 allocatePage), but if AddSchema
	// is called concurrently, we may append into wrong index
	for len(tsf.schemas) <= int(schemaId) {
		tsf.schemas = append(tsf.schemas, tsfSchema{})
	}
	tsf.schemas[schemaId] = schema
	return &tsf.schemas[schemaId]
}

// Adds entries to the file (write is deferred)
func (tsf *TSFile) AddEntries(tag TSFPageTag, entries interface{}) error {
	schemaId := tag.toSchemaId()
	if !tsf.isValidSchemaId(schemaId) {
		return fmt.Errorf("Undefined schema #%d", schemaId)
	}
	if reflect.TypeOf(entries).Kind() != reflect.Slice {
		return fmt.Errorf("Invalid AddEntries() argument, slice is expected")
	}

	start := 0
	entrySize := tsf.getEntrySize(schemaId)
	totalCount := reflect.ValueOf(entries).Len()
	for start < totalCount {
		page, pageId := tsf.getDataPage(tag)
		if page == nil {
			page, pageId = tsf.allocateDataPage(tag, 0)
		}

		count, err := page.writeEntries(start, entries, entrySize)
		if err != nil {
			return err
		}

		tsf.accountPageEntries(page, uint32(count), schemaId, pageId)

		start += count
	}

	return tsf.writePages(false)
}

// Adds content of the other file to current file
func (tsfOut *TSFile) AddFile(tsfIn *TSFile) (err error) {
	tsfIn.mu.RLock()
	defer tsfIn.mu.RUnlock()

	// Import all schemas
	schemaMap := make(map[TSFPageTag]TSFPageTag)
	for schemaIndex, schema := range tsfIn.schemas {
		inTag := TSFSchemaId(schemaIndex).toTag()
		outTag, err := tsfOut.AddSchema(&schema.header)
		if err != nil {
			return err
		}

		schemaMap[inTag] = outTag
	}

	// Import all data pages (excluding schemas, headers...)
	for pageIndex, pageHeader := range tsfIn.pageHeaders {
		inTag := TSFPageTag(pageHeader.Tag)
		if inTag < TSFTagData || pageHeader.Flags != 0 {
			continue
		}

		// Find matching schema in input/output files
		outTag, ok := schemaMap[TSFPageTag(inTag)]
		if !ok {
			return fmt.Errorf("unexpected page tag #%d: it's schema wasn't imported",
				pageHeader.Tag)
		}

		entrySize := tsfIn.getEntrySizeImpl(inTag.toSchemaId())
		outEntrySize := tsfOut.getEntrySize(outTag.toSchemaId())
		if entrySize != outEntrySize {
			return fmt.Errorf("page entry size for tag #%d is differing: %d != %d",
				inTag, entrySize, outEntrySize)
		}

		// Read page with entries from input file
		inPageId := TSFPageId(pageIndex)
		inPage := tsfIn.tryGetPage(inPageId)
		if inPage == nil {
			inPage = tsfIn.newPage(tsfIn.getPageSize(inPageId))
		}
		inPage, err = tsfIn.readPageNoLock(inPageId, inPage)
		if err != nil {
			return err
		}

		for start := 0; start < int(inPage.count); {
			outPage, outPageId := tsfOut.getDataPage(outTag)
			if outPage == nil {
				outPage, outPageId = tsfOut.allocateDataPage(outTag, 0)
			}

			count, err := outPage.copyRawEntries(start, inPage, entrySize)
			if err != nil {
				return fmt.Errorf("Cannot write to page: %v", err)
			}

			start += count
			tsfOut.accountPageEntries(outPage, uint32(count), outTag.toSchemaId(), outPageId)
		}
	}

	return nil
}

func (tsf *TSFile) getEntrySize(schemaId TSFSchemaId) uint32 {
	tsf.mu.RLock()
	defer tsf.mu.RUnlock()

	return tsf.getEntrySizeImpl(schemaId)
}

func (tsf *TSFile) getEntrySizeImpl(schemaId TSFSchemaId) uint32 {
	return uint32(tsf.schemas[schemaId].header.EntrySize)
}

// Tries to get actual data page for writing
func (tsf *TSFile) getDataPage(tag TSFPageTag) (*tsfPage, TSFPageId) {
	tsf.mu.RLock()
	defer tsf.mu.RUnlock()

	if pages, ok := tsf.dataPagesCache[tag]; ok {
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

func (tsf *TSFile) accountPageEntries(page *tsfPage, count uint32,
	schemaId TSFSchemaId, pageId TSFPageId) {
	tsf.mu.RLock()
	defer tsf.mu.RUnlock()

	// Advance page indexes that are going after our page in case
	// we are not adding to the last page
	schema := &tsf.schemas[schemaId]
	for j := len(schema.pageIndex) - 1; j >= 0; j-- {
		pageIndex := &schema.pageIndex[j]
		if pageIndex.pageId == pageId {
			for i := j + 1; i < len(schema.pageIndex); i++ {
				atomic.AddUint32(&schema.pageIndex[i].start, count)
			}
			break
		}
	}

	atomic.AddUint32(&schema.count, count)
	page.count = atomic.AddUint32(&tsf.pageHeaders[pageId].Count, count)

	if page.full {
		atomic.AddUint32(&tsf.fullPages, 1)
	}
}

func (page *tsfPage) writeEntries(start int, entries interface{}, entrySize uint32) (int, error) {
	// Write to page until it would be full
	page.mu.Lock()
	defer page.mu.Unlock()

	page.dirty = true

	buf := page.buf
	count := 0
	value := reflect.ValueOf(entries)
	for (start + count) < value.Len() {
		if (uint32(buf.Len()) + entrySize) > page.size {
			// No more space for entries in this page (and this page
			// is eligible for commiting)
			page.full = true
			break
		}

		size := buf.Len()
		v := value.Index(start + count).Interface()
		err := binary.Write(buf, binary.LittleEndian, v)
		n := buf.Len() - size

		if err != nil {
			return count, err
		}
		if uint32(n) != entrySize {
			buf.Truncate(buf.Len() - n)
			return count, fmt.Errorf("Invalid entry of size %d, %d is expected", n, entrySize)
		}

		count++
	}

	return count, nil
}

func (outPage *tsfPage) copyRawEntries(start int, inPage *tsfPage, entrySize uint32) (int, error) {
	outPage.mu.Lock()
	defer outPage.mu.Unlock()

	inPage.mu.Lock()
	defer inPage.mu.Unlock()

	count := int(inPage.count) - start
	if count < 0 {
		return count, fmt.Errorf("Start marker %d beyound page count %d", start,
			inPage.count)
	}

	startByte, endByte := uint32(start)*entrySize, inPage.count*entrySize
	availSpace := outPage.size - outPage.count*entrySize

	// Check if out page doesn't have enough space in it and if so, truncate,
	// recompute count and perform alignment
	overFlow := int32(endByte-startByte) - int32(availSpace)
	if overFlow > 0 {
		endByte -= uint32(overFlow)
		count = int((endByte - startByte) / entrySize)
		endByte = uint32(start+count) * entrySize
	}

	// Now copy bytes [start:end] from inPage to outPage
	n, err := outPage.buf.Write(inPage.buf.Bytes()[startByte:endByte])
	if err != nil {
		return 0, fmt.Errorf("Only %d bytes were copied: %v", n, err)
	}

	outPage.dirty = true
	outPage.full = (outPage.size - outPage.count*entrySize) < entrySize
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

	tsf.updateHeader(loadPageId(&tsf.headerPageId))

	tsf.mu.Lock()
	defer tsf.mu.Unlock()

	for pageId, page := range tsf.pageCache {
		if page.dirty && (page.full || sync) {
			err := tsf.writePage(page, pageId)
			if err != nil {
				return err
			}
		}

		if page.full {
			// Evict full data pages & page we don't need anymore
			tsf.evictPage(page, pageId)
		}
	}

	return nil
}

func (tsf *TSFile) getPageOffset(pageId TSFPageId) int64 {
	// Number of headers going prior to page #pageId
	numHeaders := TSFPageId(0)
	if pageId > 0 {
		switch tsf.formatFlags.getVersion() {
		case TSFFormatV1:
			numHeaders = 1
		case TSFFormatV2:
			numHeaders = (pageId + tagsPerHeader - 1) / tagsPerHeader
		}
	}
	offset := uint32(pageId-numHeaders)*tsf.pageSize + uint32(numHeaders)*pageSize

	return int64(offset)
}

func (tsf *TSFile) getPageSize(pageId TSFPageId) uint32 {
	if tsf.formatFlags.getVersion() == TSFFormatV1 {
		if pageId == 0 {
			return pageSize
		}
		return tsf.pageSize
	}

	return pageSize
}

// Write page (call with tsf.mu locked)
func (tsf *TSFile) writePage(page *tsfPage, pageId TSFPageId) error {
	// Should be called for full page, header page or during close
	// so we shouldn't lock page.mu here as nobody except us should write
	// into page buffer

	_, err := tsf.file.Seek(tsf.getPageOffset(pageId), io.SeekStart)
	if err != nil {
		return err
	}

	// Pad page up to its size for v2+
	buf := page.buf.Bytes()
	if tsf.formatFlags.getVersion() == TSFFormatV2 {
		padLength := int(page.size) - len(buf)
		if padLength > 0 {
			buf = append(buf, bytes.Repeat([]byte{0}, padLength)...)
		}
	}

	_, err = tsf.file.Write(buf)

	page.dirty = false
	return err
}

// Returns first and last tags (non-inclusive) which have schemas
func (tsf *TSFile) GetDataTags() (TSFPageTag, TSFPageTag) {
	tsf.mu.RLock()
	defer tsf.mu.RUnlock()

	return TSFTagData, TSFTagData + TSFPageTag(len(tsf.schemas))
}

// Returns schema header of throws error if such schema doesn't exist
func (tsf *TSFile) GetSchema(pageTag TSFPageTag) (*TSFSchemaHeader, error) {
	tsf.mu.RLock()
	defer tsf.mu.RUnlock()

	schemaId := int(pageTag.toSchemaId())
	if schemaId >= len(tsf.schemas) {
		return nil, fmt.Errorf("Schema #%d doesn't exist", schemaId)
	}

	return &tsf.schemas[schemaId].header, nil
}

// Get number of entries for schema. If schema doesn't exist,
// returns -1
func (tsf *TSFile) GetEntryCount(pageTag TSFPageTag) int {
	tsf.mu.RLock()
	defer tsf.mu.RUnlock()

	schemaId := int(pageTag.toSchemaId())
	if schemaId >= len(tsf.schemas) {
		return -1
	}
	return int(atomic.LoadUint32(&tsf.schemas[schemaId].count))
}

// Cumulative function which returns info provided by GetEntryCount(),
// GetDataTags() and schema names
func (tsf *TSFile) GetStats() (stats TSFileStats) {
	tsf.mu.RLock()
	defer tsf.mu.RUnlock()

	stats.Series = make([]TSFSeriesStats, len(tsf.schemas))
	for schemaIndex, schema := range tsf.schemas {
		seriesStat := &stats.Series[schemaIndex]

		seriesStat.Tag = TSFSchemaId(schemaIndex).toTag()
		seriesStat.Name = DecodeCStr(schema.header.Name[:])
		seriesStat.Count = uint(schema.count)
	}

	return
}

// Gets entries of type tag starting at position start. If entries
// is not a slice, panics
func (tsf *TSFile) GetEntries(tag TSFPageTag, entries interface{}, start int) error {
	if reflect.TypeOf(entries).Kind() != reflect.Slice {
		panic("Unexpected arguemnt to GetEntries(), should be slice")
	}

	value := reflect.ValueOf(entries)
	count := value.Len()
	isBufferSlice := (value.Type().Elem() == reflect.TypeOf([]byte{}))
	entrySize := 1
	data := value

	if isBufferSlice {
		// binary.Read doesn't natively supports [][]uint8, so we're implementing
		// it on our own
		schema, err := tsf.GetSchema(tag)
		if err != nil {
			return err
		}

		entrySize = int(schema.EntrySize)
		data = reflect.MakeSlice(reflect.TypeOf([]uint8{}), count*entrySize, count*entrySize)
	}

	var offset int
	for count > 0 {
		pageId, byteOffset, err := tsf.findDataPage(tag, start)
		if err != nil {
			return err
		}

		page, err := tsf.readPage(pageId)
		if err != nil {
			return err
		}

		// Determine how many entries we can read from this page
		pageCount := count
		if pageCount > int(page.count) {
			pageCount = int(page.count)
		}
		if pageCount == 0 {
			return fmt.Errorf("Page #%d is empty, this is unexpected", pageId)
		}

		slice := data.Slice(offset*entrySize, (offset+pageCount)*entrySize)
		err = page.read(slice.Interface(), byteOffset)
		if err != nil {
			return fmt.Errorf("Error reading page #%d: %v", pageId, err)
		}

		start += pageCount
		count -= pageCount
		offset += pageCount
	}

	if isBufferSlice {
		// Cut buffer into smaller slices and assign them to buffer
		for i := 0; i < value.Len(); i++ {
			value.Index(i).Set(data.Slice(i*entrySize, (i+1)*entrySize))
		}
	}

	return nil
}

// Finds pageId with entry id and offset inside it or returns zeroes if not found
func (tsf *TSFile) findDataPage(tag TSFPageTag, index int) (TSFPageId, int64, error) {
	tsf.mu.RLock()
	defer tsf.mu.RUnlock()

	schemaId := tag.toSchemaId()
	if !tsf.isValidSchemaId(schemaId) {
		return 0, 0, fmt.Errorf("Schema for tag %d doesn't exist", tag)
	}

	schema := &tsf.schemas[schemaId]
	if int(schema.count) <= index {
		return 0, 0, fmt.Errorf("Entry #%d is out of range [0;%d)", index, int(schema.count))
	}

	// Align index by entries per page count to find entry in index
	// If entry wasn't found, some pages were not complete, so we seek
	// into index using iteration
	entrySize := int(schema.header.EntrySize)
	if tsf.formatFlags.getVersion() == TSFFormatV1 {
		entriesPerPage := (int(tsf.pageSize) / entrySize)
		pageId := index / entriesPerPage
		offset := (index - pageId*entriesPerPage) * entrySize
		return TSFPageId(pageId + 1), int64(offset), nil
	}

	// Binary search page in pageIndex
	a, b := 0, len(schema.pageIndex)
	for b > a {
		med := a + (b-a)/2
		pageIndex := schema.pageIndex[med]
		count := int(tsf.pageHeaders[pageIndex.pageId].Count)
		start := int(pageIndex.start)

		if start <= index {
			if index < (start + count) {
				offset := (index - start) * entrySize
				return pageIndex.pageId, int64(offset), nil
			}
			a = med + 1
		} else {
			b = med
		}
	}

	// Did not found in index -- impossible!
	return 0, 0, fmt.Errorf("Index for entry #%d was not found (internal error)",
		index)
}

// Evicts page from page cache. Called with mu locked
func (tsf *TSFile) evictPage(page *tsfPage, pageId TSFPageId) {
	if pageId == tsf.headerPageId {
		return
	}

	delete(tsf.pageCache, pageId)

	pageTag := TSFPageTag(tsf.pageHeaders[pageId].Tag)
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

// Fetch page from file or take it from page cache
func (tsf *TSFile) readPage(pageId TSFPageId) (*tsfPage, error) {
	page := tsf.tryGetPage(pageId)
	if page != nil {
		return page, nil
	}
	if pageId != 0 && pageId >= loadPageId(&tsf.pageCount) {
		return nil, fmt.Errorf("File doesn't have page %d", pageId)
	}

	page = tsf.newPage(tsf.getPageSize(pageId))

	tsf.mu.Lock()
	defer tsf.mu.Unlock()

	return tsf.readPageNoLock(pageId, page)
}

func (tsf *TSFile) readPageNoLock(pageId TSFPageId, page *tsfPage) (*tsfPage, error) {
	// second check under strictier lock
	if page, ok := tsf.pageCache[pageId]; ok {
		return page, nil
	}

	// allocate buffer -- in the case of data page which is not full,
	// buffer size should be partial (so we can append to buffer)
	pageSize := page.size
	if pageId > 0 {
		hdr := &tsf.pageHeaders[pageId]
		if hdr.Flags == 0 && hdr.getTag().isDataTag() {
			pageSize = hdr.Count * tsf.getEntrySizeImpl(hdr.getTag().toSchemaId())
		}
		page.count = hdr.Count
	}
	buf := make([]byte, pageSize)

	// really read from file
	_, err := tsf.file.Seek(tsf.getPageOffset(pageId), io.SeekStart)
	if err != nil {
		return nil, err
	}

	n, err := tsf.file.Read(buf)
	if err != nil {
		return nil, err
	}
	if uint32(n) != pageSize && (tsf.formatFlags.getVersion() == TSFFormatV2) {
		return nil, fmt.Errorf("Invalid read of size %d for page %d (requested size: %d)",
			n, pageId, pageSize)
	}

	// setup page and return it. header #0 is a special case...
	page.buf = bytes.NewBuffer(buf)

	tsf.tryEvictPages()
	tsf.pageCache[pageId] = page
	return page, nil
}

func (tsf *TSFile) tryGetPage(pageId TSFPageId) *tsfPage {
	tsf.mu.RLock()
	defer tsf.mu.RUnlock()

	if page, ok := tsf.pageCache[pageId]; ok {
		return page
	}
	return nil
}

// Evict some pages. Call with mu held
func (tsf *TSFile) tryEvictPages() {
	if len(tsf.pageCache) < maxCachedPagesHigh {
		return
	}

	evictGen := atomic.LoadUint32(&tsf.pageGeneration) - maxCachedPagesLow
	for pageId, page := range tsf.pageCache {
		if page.generation < evictGen && !page.dirty {
			tsf.evictPage(page, pageId)
		}
	}
}

// Reads data from a page at offset off
func (page *tsfPage) read(data interface{}, off int64) error {
	page.mu.Lock()
	defer page.mu.Unlock()

	reader := bytes.NewReader(page.buf.Bytes())
	reader.Seek(off, 0)

	return binary.Read(reader, binary.LittleEndian, data)
}

// Rewrites header page and marks it as full
func (tsf *TSFile) updateHeader(pageId TSFPageId) *tsfPage {
	// Update super block (up to 4 can do it concurrently)
	sbIndex := atomic.AddUint32(&tsf.sbIndex, 1)
	sb := &tsf.header.SuperBlocks[sbIndex%superBlockCount]
	sb.Time = uint64(time.Now().UnixNano())

	tsf.mu.Lock()
	defer tsf.mu.Unlock()

	if page, ok := tsf.pageCache[pageId]; ok {
		buf := page.buf
		buf.Reset()

		switch tsf.formatFlags.getVersion() {
		case TSFFormatV1:
			tsf.updateHeaderV1(pageId, sb, buf)
		case TSFFormatV2:
			tsf.updateHeaderV2(pageId, sb, buf)
		}

		page.full = true
		page.dirty = true
		return page
	}

	return nil
}

func (tsf *TSFile) updateHeaderV1(pageId TSFPageId, sb *TSFSuperBlock, buf *bytes.Buffer) {
	sb.Count = atomic.LoadUint32(&tsf.schemas[0].count)

	binary.Write(buf, binary.LittleEndian, tsf.header)
	binary.Write(buf, binary.LittleEndian, tsf.schemas[0].header)
}

func (tsf *TSFile) updateHeaderV2(pageId TSFPageId, sb *TSFSuperBlock, buf *bytes.Buffer) {
	// Select range of page headers corresponding to this header
	startPage := int(pageId)
	if startPage > 0 {
		// Header 0 is the exception which refers itself and next header
		// in page tag array.
		startPage++
	}
	endPage := len(tsf.pageHeaders)
	if endPage > startPage+tagsPerHeader {
		endPage = startPage + tagsPerHeader
	}

	// Cannot use pageCount here as we might allocate some pages that are referred
	// by a new header, not this header
	sb.Count = uint32(endPage)

	binary.Write(buf, binary.LittleEndian, tsf.header)
	binary.Write(buf, binary.LittleEndian, tsf.pageHeaders[startPage:endPage])
}

// Allocates new page: inserts page header to and page object and returns
// page along with its index
func (tsf *TSFile) allocateDataPage(tag TSFPageTag, flags uint) (*tsfPage, TSFPageId) {
	page := tsf.newPage(tsf.pageSize)

	pageId := nextPageId(&tsf.pageCount)
	nextHeaderId := loadPageId(&tsf.headerPageId) + TSFPageId(tagsPerHeader)
	if nextHeaderId == tagsPerHeader {
		// First header has index 0, but second should fit to first header's
		// area, so it has index tagsPerHeader-1
		nextHeaderId--
	}
	if (tsf.formatFlags&TSFFormatV2) != 0 && pageId == nextHeaderId {
		// This should be a header page, so we can start new extent
		oldHeaderPageId := swapPageId(&tsf.headerPageId, pageId)
		tsf.insertPage(tsf.newPage(pageSize), pageId, TSFTagHeader, 0)

		pageId = nextPageId(&tsf.pageCount)

		// Make sure that previous header have reference to a new header in
		// its page tag table
		tsf.updateHeader(oldHeaderPageId)
	}

	return tsf.insertPage(page, pageId, tag, flags)
}

func (tsf *TSFile) newPage(size uint32) *tsfPage {
	page := new(tsfPage)
	page.buf = bytes.NewBuffer([]byte{})
	page.size = size
	page.buf.Grow(int(size))
	page.generation = atomic.AddUint32(&tsf.pageGeneration, 1)

	return page
}

func (tsf *TSFile) insertPage(page *tsfPage, pageId TSFPageId, tag TSFPageTag, flags uint) (*tsfPage, TSFPageId) {
	// Insert page to a list of pages and update header
	tsf.mu.Lock()
	defer tsf.mu.Unlock()

	for int(pageId) >= len(tsf.pageHeaders) {
		tsf.pageHeaders = append(tsf.pageHeaders,
			make([]TSFPageHeader, initialPageCount)...)
	}

	if tag.isDataTag() && tsf.isValidSchemaId(tag.toSchemaId()) && flags == 0 {
		// Save page id into per-schema index.
		// XXX: in case of concurrent inserts, wouldn't we need to update pageIndex
		// of following pages?
		schema := &tsf.schemas[tag.toSchemaId()]
		schema.pageIndex = append(schema.pageIndex, tsfPageIndex{
			pageId: pageId,
			start:  schema.count,
		})
	}

	tsf.pageHeaders[pageId].Tag = uint16(tag)
	tsf.pageHeaders[pageId].Flags = uint16(flags)
	tsf.pageCache[pageId] = page

	if tag >= 0 && flags == 0 {
		// If we accidentally created copy of data page, it is not bad,
		// we're simply save second page for convenience and take first
		pages := tsf.dataPagesCache[tag]

		if len(pages) == 0 {
			tsf.dataPagesCache[tag] = []TSFPageId{pageId}
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

// A helper which encodes go string as C string and copies to dst (slice of
// the byte array)
func EncodeCStr(goStr string, dst []byte) int {
	src := []byte(goStr)

	copy(dst, src)
	zIndex := len(dst) - 1
	if len(src) < zIndex {
		zIndex = len(src)
	}

	dst[zIndex] = '\000'
	return zIndex
}

package blueprints

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// NBT Tag Type Constants
const (
	tagEnd       byte = 0
	tagByte      byte = 1
	tagShort     byte = 2
	tagInt       byte = 3
	tagLong      byte = 4
	tagFloat     byte = 5
	tagDouble    byte = 6
	tagByteArray byte = 7
	tagString    byte = 8
	tagList      byte = 9
	tagCompound  byte = 10
	tagIntArray  byte = 11
	tagLongArray byte = 12
)

var (
	ErrInvalidNBTFormat = errors.New("invalid NBT structure")
	ErrUnsupportedTag   = errors.New("unsupported NBT tag")
)

// LoadSchematic reads a gzipped .schem or .nbt file into a 3D VoxelMatrix.
func LoadSchematic(path string) (*VoxelMatrix, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening schematic file %s: %w", path, err)
	}
	defer f.Close()

	return ParseSchematic(f)
}

// ParseSchematic decodes a gzipped or uncompressed Sponge/WorldEdit .schem stream into a VoxelMatrix.
func ParseSchematic(r io.Reader) (*VoxelMatrix, error) {
	// Read header to check for gzip magic numbers (0x1f, 0x8b)
	header := make([]byte, 2)
	n, err := io.ReadFull(r, header)
	if err != nil {
		return nil, fmt.Errorf("reading header: %w", err)
	}

	combined := io.MultiReader(bytes.NewReader(header[:n]), r)
	var reader io.Reader = combined

	if header[0] == 0x1f && header[1] == 0x8b {
		gz, err := gzip.NewReader(combined)
		if err != nil {
			return nil, fmt.Errorf("creating gzip reader: %w", err)
		}
		defer gz.Close()
		reader = gz
	}

	root, err := readNBT(reader)
	if err != nil {
		return nil, fmt.Errorf("reading NBT: %w", err)
	}

	rootCompound, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: root tag must be compound", ErrInvalidNBTFormat)
	}

	// Some schematics wrap payload in a "Schematic" root key
	if schem, ok := rootCompound["Schematic"].(map[string]any); ok {
		rootCompound = schem
	}

	// Width, Height, Length
	width := getIntField(rootCompound, "Width")
	height := getIntField(rootCompound, "Height")
	length := getIntField(rootCompound, "Length")

	if width <= 0 || height <= 0 || length <= 0 {
		return nil, fmt.Errorf("%w: invalid dimensions W=%d, H=%d, L=%d", ErrInvalidDimensions, width, height, length)
	}

	vm := NewVoxelMatrix(width, height, length)

	// Sponge Schematic Palette: map[string]int (blockState -> id)
	paletteMap := make(map[int]string)
	if pal, ok := rootCompound["Palette"].(map[string]any); ok {
		for stateStr, idVal := range pal {
			id := toInt(idVal)
			paletteMap[id] = stateStr
		}
	}

	// Sponge Schematic BlockData: byte array (varints)
	if blockData, ok := rootCompound["BlockData"].([]byte); ok {
		// Parse varints
		buf := bytes.NewReader(blockData)
		for y := 0; y < height; y++ {
			for z := 0; z < length; z++ {
				for x := 0; x < width; x++ {
					palIdx, err := readVarInt(buf)
					if err != nil {
						// If truncated, stop
						break
					}
					if rawState, ok := paletteMap[palIdx]; ok && rawState != "" {
						_ = vm.Set(x, y, z, ParseBlockState(rawState))
					}
				}
			}
		}
	} else if intBlocks, ok := rootCompound["BlockData"].([]int32); ok {
		idx := 0
		for y := 0; y < height; y++ {
			for z := 0; z < length; z++ {
				for x := 0; x < width; x++ {
					if idx < len(intBlocks) {
						palIdx := int(intBlocks[idx])
						idx++
						if rawState, ok := paletteMap[palIdx]; ok && rawState != "" {
							_ = vm.Set(x, y, z, ParseBlockState(rawState))
						}
					}
				}
			}
		}
	}

	return vm, nil
}

// SaveSchematic serializes a VoxelMatrix to a gzipped Sponge .schem file.
func SaveSchematic(path string, vm *VoxelMatrix) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating schematic file %s: %w", path, err)
	}
	defer f.Close()

	return WriteSchematic(f, vm)
}

// WriteSchematic writes a VoxelMatrix in Sponge .schem format to a writer.
func WriteSchematic(w io.Writer, vm *VoxelMatrix) error {
	if vm == nil {
		return ErrNilBlueprint
	}

	gz := gzip.NewWriter(w)
	defer gz.Close()

	// 1. Build palette
	palette := make(map[string]int)
	palette["minecraft:air"] = 0
	nextID := 1

	for y := 0; y < vm.Dimensions.Height; y++ {
		for z := 0; z < vm.Dimensions.Length; z++ {
			for x := 0; x < vm.Dimensions.Width; x++ {
				st := vm.voxels[y][z][x]
				if st.IsAir() {
					continue
				}
				str := st.String()
				if _, exists := palette[str]; !exists {
					palette[str] = nextID
					nextID++
				}
			}
		}
	}

	// 2. Build BlockData varints
	var blockData bytes.Buffer
	for y := 0; y < vm.Dimensions.Height; y++ {
		for z := 0; z < vm.Dimensions.Length; z++ {
			for x := 0; x < vm.Dimensions.Width; x++ {
				st := vm.voxels[y][z][x]
				palIdx := 0
				if !st.IsAir() {
					palIdx = palette[st.String()]
				}
				writeVarInt(&blockData, palIdx)
			}
		}
	}

	// 3. Write Root Compound Tag
	if _, err := gz.Write([]byte{tagCompound}); err != nil {
		return err
	}
	if err := writeNBTString(gz, "Schematic"); err != nil {
		return err
	}

	// Version = 2
	if err := writeNBTInt(gz, "Version", 2); err != nil {
		return err
	}
	// DataVersion = 3955 (1.21.1)
	if err := writeNBTInt(gz, "DataVersion", 3955); err != nil {
		return err
	}
	// Dimensions
	if err := writeNBTShort(gz, "Width", int16(vm.Dimensions.Width)); err != nil {
		return err
	}
	if err := writeNBTShort(gz, "Height", int16(vm.Dimensions.Height)); err != nil {
		return err
	}
	if err := writeNBTShort(gz, "Length", int16(vm.Dimensions.Length)); err != nil {
		return err
	}

	// Palette Compound
	if _, err := gz.Write([]byte{tagCompound}); err != nil {
		return err
	}
	if err := writeNBTString(gz, "Palette"); err != nil {
		return err
	}
	palKeys := make([]string, 0, len(palette))
	for k := range palette {
		palKeys = append(palKeys, k)
	}
	sort.Strings(palKeys)
	for _, k := range palKeys {
		if err := writeNBTInt(gz, k, int32(palette[k])); err != nil {
			return err
		}
	}
	if _, err := gz.Write([]byte{tagEnd}); err != nil {
		return err
	}

	// BlockData ByteArray
	if _, err := gz.Write([]byte{tagByteArray}); err != nil {
		return err
	}
	if err := writeNBTString(gz, "BlockData"); err != nil {
		return err
	}
	bytesSlice := blockData.Bytes()
	if err := binary.Write(gz, binary.BigEndian, int32(len(bytesSlice))); err != nil {
		return err
	}
	if _, err := gz.Write(bytesSlice); err != nil {
		return err
	}

	// Root Compound End
	if _, err := gz.Write([]byte{tagEnd}); err != nil {
		return err
	}

	return nil
}

// Minimal NBT Reader
func readNBT(r io.Reader) (any, error) {
	tagTypeBuf := make([]byte, 1)
	if _, err := io.ReadFull(r, tagTypeBuf); err != nil {
		return nil, err
	}
	tagType := tagTypeBuf[0]
	if tagType == tagEnd {
		return nil, nil
	}

	// Read root name
	_, err := readNBTString(r)
	if err != nil {
		return nil, err
	}

	return readNBTValue(r, tagType)
}

func readNBTValue(r io.Reader, tagType byte) (any, error) {
	switch tagType {
	case tagByte:
		var v int8
		err := binary.Read(r, binary.BigEndian, &v)
		return v, err
	case tagShort:
		var v int16
		err := binary.Read(r, binary.BigEndian, &v)
		return v, err
	case tagInt:
		var v int32
		err := binary.Read(r, binary.BigEndian, &v)
		return v, err
	case tagLong:
		var v int64
		err := binary.Read(r, binary.BigEndian, &v)
		return v, err
	case tagFloat:
		var v float32
		err := binary.Read(r, binary.BigEndian, &v)
		return v, err
	case tagDouble:
		var v float64
		err := binary.Read(r, binary.BigEndian, &v)
		return v, err
	case tagByteArray:
		var length int32
		if err := binary.Read(r, binary.BigEndian, &length); err != nil {
			return nil, err
		}
		data := make([]byte, length)
		_, err := io.ReadFull(r, data)
		return data, err
	case tagString:
		return readNBTString(r)
	case tagList:
		var elemType byte
		var length int32
		if err := binary.Read(r, binary.BigEndian, &elemType); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.BigEndian, &length); err != nil {
			return nil, err
		}
		list := make([]any, length)
		for i := int32(0); i < length; i++ {
			val, err := readNBTValue(r, elemType)
			if err != nil {
				return nil, err
			}
			list[i] = val
		}
		return list, nil
	case tagCompound:
		compound := make(map[string]any)
		for {
			tBuf := make([]byte, 1)
			if _, err := io.ReadFull(r, tBuf); err != nil {
				return nil, err
			}
			childType := tBuf[0]
			if childType == tagEnd {
				break
			}
			name, err := readNBTString(r)
			if err != nil {
				return nil, err
			}
			val, err := readNBTValue(r, childType)
			if err != nil {
				return nil, err
			}
			compound[name] = val
		}
		return compound, nil
	case tagIntArray:
		var length int32
		if err := binary.Read(r, binary.BigEndian, &length); err != nil {
			return nil, err
		}
		data := make([]int32, length)
		if err := binary.Read(r, binary.BigEndian, data); err != nil {
			return nil, err
		}
		return data, nil
	default:
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedTag, tagType)
	}
}

func readNBTString(r io.Reader) (string, error) {
	var length uint16
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return "", err
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func writeNBTString(w io.Writer, s string) error {
	bytes := []byte(s)
	if err := binary.Write(w, binary.BigEndian, uint16(len(bytes))); err != nil {
		return err
	}
	_, err := w.Write(bytes)
	return err
}

func writeNBTShort(w io.Writer, name string, val int16) error {
	if _, err := w.Write([]byte{tagShort}); err != nil {
		return err
	}
	if err := writeNBTString(w, name); err != nil {
		return err
	}
	return binary.Write(w, binary.BigEndian, val)
}

func writeNBTInt(w io.Writer, name string, val int32) error {
	if _, err := w.Write([]byte{tagInt}); err != nil {
		return err
	}
	if err := writeNBTString(w, name); err != nil {
		return err
	}
	return binary.Write(w, binary.BigEndian, val)
}

func readVarInt(r io.ByteReader) (int, error) {
	var result int
	var shift uint
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		result |= int(b&0x7F) << shift
		if (b & 0x80) == 0 {
			break
		}
		shift += 7
		if shift >= 32 {
			return 0, errors.New("varint overflow")
		}
	}
	return result, nil
}

func writeVarInt(w *bytes.Buffer, value int) {
	u := uint(value)
	for {
		b := byte(u & 0x7F)
		u >>= 7
		if u != 0 {
			b |= 0x80
		}
		w.WriteByte(b)
		if u == 0 {
			break
		}
	}
}

func getIntField(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	return toInt(v)
}

func toInt(v any) int {
	switch val := v.(type) {
	case int:
		return val
	case int8:
		return int(val)
	case int16:
		return int(val)
	case int32:
		return int(val)
	case int64:
		return int(val)
	case uint8:
		return int(val)
	case uint16:
		return int(val)
	case uint32:
		return int(val)
	case uint64:
		return int(val)
	case float64:
		return int(val)
	default:
		return 0
	}
}

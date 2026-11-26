package craftingtest

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// ServerJarEnv names a Minecraft data jar to load the real catalogue from.
const ServerJarEnv = "CIVQUEST_MINECRAFT_DATA_JAR"

// FindServerJar returns the data jar of a local dedicated server install, or "". Mojang's
// recipe data is not checked in, so tests over the real catalogue skip when it is absent.
func FindServerJar() string {
	if p := os.Getenv(ServerJarEnv); p != "" {
		return p
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		data = filepath.Join(home, ".local", "share")
	}
	found, _ := filepath.Glob(filepath.Join(data, "civquest", "server", "libraries", "net", "minecraft", "server", "*", "server-*-extra.jar"))
	if len(found) == 0 {
		return ""
	}
	sort.Strings(found)
	return found[len(found)-1]
}

type ingredient []struct {
	Item string `json:"item"`
	Tag  string `json:"tag"`
}

func parseIngredient(raw json.RawMessage) (ingredient, error) {
	var many ingredient
	if len(raw) > 0 && raw[0] == '[' {
		return many, json.Unmarshal(raw, &many)
	}
	var single struct {
		Item string `json:"item"`
		Tag  string `json:"tag"`
	}
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, err
	}
	return append(many, single), nil
}

// FromServerJar reads the crafting recipes and item tags of a Minecraft 1.21.1 data jar.
func FromServerJar(jar string) ([]*civquestv1.RecipeNode, error) {
	z, err := zip.OpenReader(jar)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	read := func(f *zip.File, into any) error {
		r, err := f.Open()
		if err != nil {
			return err
		}
		defer r.Close()
		b, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, into)
	}

	const tagDir, recipeDir = "data/minecraft/tags/item/", "data/minecraft/recipe/"
	tags := map[string][]string{}
	var recipes []*zip.File
	for _, f := range z.File {
		switch {
		case strings.HasPrefix(f.Name, tagDir) && strings.HasSuffix(f.Name, ".json"):
			var t struct {
				Values []string `json:"values"`
			}
			if err := read(f, &t); err != nil {
				return nil, err
			}
			tags["minecraft:"+strings.TrimSuffix(strings.TrimPrefix(f.Name, tagDir), ".json")] = t.Values
		case strings.HasPrefix(f.Name, recipeDir) && strings.HasSuffix(f.Name, ".json"):
			recipes = append(recipes, f)
		}
	}
	var expand func(ing ingredient) []string
	expand = func(ing ingredient) []string {
		var out []string
		for _, i := range ing {
			if i.Item != "" {
				out = append(out, i.Item)
			}
			for _, v := range tags[i.Tag] {
				if nested, ok := strings.CutPrefix(v, "#"); ok {
					out = append(out, expand(ingredient{{Tag: nested}})...)
				} else {
					out = append(out, v)
				}
			}
		}
		return out
	}

	sort.Slice(recipes, func(i, j int) bool { return recipes[i].Name < recipes[j].Name })
	var nodes []*civquestv1.RecipeNode
	for _, f := range recipes {
		var r struct {
			Type        string                     `json:"type"`
			Pattern     []string                   `json:"pattern"`
			Key         map[string]json.RawMessage `json:"key"`
			Ingredients []json.RawMessage          `json:"ingredients"`
			Result      struct {
				ID    string `json:"id"`
				Count int32  `json:"count"`
			} `json:"result"`
		}
		// Non-crafting recipe types use other result shapes; skip whatever does not fit.
		if err := read(f, &r); err != nil || r.Result.ID == "" {
			continue
		}
		var raws []json.RawMessage
		var counts []int32
		big := false
		switch r.Type {
		case "minecraft:crafting_shaped":
			used := map[rune]int32{}
			for _, row := range r.Pattern {
				big = big || len(row) > 2
				for _, ch := range row {
					if ch != ' ' {
						used[ch]++
					}
				}
			}
			big = big || len(r.Pattern) > 2
			keys := make([]string, 0, len(r.Key))
			for k := range r.Key {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				raws = append(raws, r.Key[k])
				counts = append(counts, used[[]rune(k)[0]])
			}
		case "minecraft:crafting_shapeless":
			big = len(r.Ingredients) > 4
			seen := map[string]int{}
			for _, raw := range r.Ingredients {
				if i, ok := seen[string(raw)]; ok {
					counts[i]++
					continue
				}
				seen[string(raw)] = len(raws)
				raws = append(raws, raw)
				counts = append(counts, 1)
			}
		default:
			continue
		}
		n := &civquestv1.RecipeNode{
			RecipeId: "minecraft:" + strings.TrimSuffix(path.Base(f.Name), ".json"), RecipeType: r.Type, Requires_3X3: big,
			Result: &civquestv1.ItemStackProto{ItemId: r.Result.ID, Count: max(r.Result.Count, 1)},
		}
		if big {
			n.WorkstationBlock = "minecraft:crafting_table"
		}
		for i, raw := range raws {
			ing, err := parseIngredient(raw)
			if err != nil {
				return nil, err
			}
			n.IngredientSlots = append(n.IngredientSlots, &civquestv1.RecipeIngredientSlot{AcceptedItemIds: expand(ing), Count: counts[i]})
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

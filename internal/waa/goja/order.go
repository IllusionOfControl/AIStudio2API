package goja

import "github.com/Mag1cFall/AIStudio2API/internal/waa/goja/unistring"

// orderOwnKeys reorders object string keys by SpiderMonkey definition order, keeping unlisted keys in their relative order afterward
func (o *baseObject) orderOwnKeys(names ...string) {
	listed := make(map[unistring.String]bool, len(names))
	ordered := make([]unistring.String, 0, len(o.propNames))
	for _, name := range names {
		key := unistring.NewFromString(name)
		if _, exists := o.values[key]; exists && !listed[key] {
			listed[key] = true
			ordered = append(ordered, key)
		}
	}
	for _, key := range o.propNames {
		if !listed[key] {
			ordered = append(ordered, key)
		}
	}
	o.propNames = ordered
	o.lastSortedPropLen, o.idxPropCount = 0, 0
}

// orderOwnKeys materializes templated properties and reorders string keys in given order
func (o *templatedObject) orderOwnKeys(names ...string) {
	o.materialiseProps()
	o.baseObject.orderOwnKeys(names...)
}

// OrderOwnKeys reorders own string keys of object in given order, keeping unlisted keys in their relative order afterward
func (o *Object) OrderOwnKeys(names []string) {
	if target, ok := o.self.(interface{ orderOwnKeys(...string) }); ok {
		target.orderOwnKeys(names...)
	}
}

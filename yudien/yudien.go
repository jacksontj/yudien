package yudien

import (
	"fmt"
	"strings"
	"encoding/json"
	"database/sql"
	"log"
	"github.com/segmentio/ksuid"
	"io/ioutil"
	. "github.com/ghowland/yudien/yudienutil"
	. "github.com/ghowland/yudien/yudiendata"
	. "github.com/ghowland/yudien/yudiencore"
)

const (
	part_unknown  = iota
	part_function = iota
	part_item     = iota
	part_string   = iota
	part_compound = iota
	part_list     = iota
	part_map      = iota
	part_map_key  = iota
)

const (
	type_int				= iota
	type_float				= iota
	type_string				= iota
	type_string_force		= iota	// This forces it to a string, even if it will be ugly, will print the type of the non-string data too.  Testing this to see if splitting these into 2 yields better results.
	type_array				= iota	// []interface{} - takes: lists, arrays, maps (key/value tuple array, strings (single element array), ints (single), floats (single)
	type_map				= iota	// map[string]interface{}
)



func DescribeUdnPart(part *UdnPart) string {

	depth_margin := strings.Repeat("  ", part.Depth)

	output := ""

	if part.PartType == part_function {
		output += fmt.Sprintf("%s%s: %-20s [%s]\n", depth_margin, PartTypeName[part.PartType], part.Value, part.Id)
	} else {
		output += fmt.Sprintf("%s%s: %-20s\n", depth_margin, PartTypeName[part.PartType], part.Value)
	}

	if part.BlockBegin != nil {
		output += fmt.Sprintf("%sBlock:  Begin: %s   End: %s\n", depth_margin, part.BlockBegin.Id, part.BlockEnd.Id)
	}

	if part.Children.Len() > 0 {
		output += fmt.Sprintf("%sArgs: %d\n", depth_margin, part.Children.Len())
		for child := part.Children.Front(); child != nil; child = child.Next() {
			output += DescribeUdnPart(child.Value.(*UdnPart))
		}
	}

	if part.NextUdnPart != nil {
		output += fmt.Sprintf("%sNext Command:\n", depth_margin)
		output += DescribeUdnPart(part.NextUdnPart)
	}

	return output
}

// Execution group allows for Blocks to be run concurrently.  A Group has Concurrent Blocks, which has UDN pairs of strings, so 3 levels of arrays for grouping
type UdnExecutionGroup struct {
	Blocks [][][]string
}

type UdnFunc func(db *sql.DB, udn_schema map[string]interface{}, udn_start *UdnPart, args []interface{}, input interface{}, udn_data map[string]interface{}) UdnResult

var UdnFunctions = map[string]UdnFunc{}


func InitUdn() {
	Debug_Udn_Api = true
	Debug_Udn = false

	UdnFunctions = map[string]UdnFunc{
		"__query":        UDN_QueryById,
		"__debug_output": UDN_DebugOutput,
		"__if":           UDN_IfCondition,
		"__end_if":       nil,
		"__else":         UDN_ElseCondition,
		"__end_else":     nil,
		"__else_if":      UDN_ElseIfCondition,
		"__end_else_if":  nil,
		"__not":          UDN_Not,
		"__not_nil":          UDN_NotNil,
		"__iterate":      UDN_Iterate,
		"__end_iterate":  nil,
		"__get":          UDN_Get,
		"__set":          UDN_Set,
		"__get_first":          UDN_GetFirst,		// Takes N strings, which are dotted for udn_data accessing.  The first value that isnt nil is returned.  nil is returned if they all are
		"__get_temp":          UDN_GetTemp,			// Function stack based temp storage
		"__set_temp":          UDN_SetTemp,			// Function stack based temp storage
		//"__temp_clear":          UDN_ClearTemp,
		//"__watch": UDN_WatchSyncronization,
		//"___watch_timeout": UDN_WatchTimeout,				//TODO(g): Should this just be an arg to __watch?  I think so...  Like if/else, watch can control the flow...
		//"__end_watch": nil,
		"__test_return":           UDN_TestReturn, // Return some data as a result
		"__test":           UDN_Test,
		"__test_different": UDN_TestDifferent,
		// Migrating from old functions
		//TODO(g): These need to be reviewed, as they are not necessarily the best way to do this, this is just the easiest/fastest way to do this
		"__widget": UDN_Widget,
		// New functions for rendering web pages (finally!)
		//"__template": UDN_StringTemplate,					// Does a __get from the args...
		"__template": UDN_StringTemplateFromValue,					// Does a __get from the args...
		"__template_wrap": UDN_StringTemplateMultiWrap,					// Takes N-2 tuple args, after 0th arg, which is the wrap_key, (also supports a single arg templating, like __template, but not the main purpose).  For each N-Tuple, the new map data gets "value" set by the previous output of the last template, creating a rolling "wrap" function.
		"__template_map": UDN_MapTemplate,		//TODO(g): Like format, for templating.  Takes 3*N args: (key,text,map), any number of times.  Performs template and assigns key into the input map
		"__format": UDN_MapStringFormat,			//TODO(g): Updates a map with keys and string formats.  Uses the map to format the strings.  Takes N args, doing each arg in sequence, for order control
		"__template_short": UDN_StringTemplateFromValueShort,		// Like __template, but uses {{{fieldname}}} instead of {{index .Max "fieldname"}}, using strings.Replace instead of text/template


		//TODO(g): DEPRICATE.  Longer name, same function.
		"__template_string": UDN_StringTemplateFromValue,	// Templates the string passed in as arg_0

		"__string_append": UDN_StringAppend,
		"__string_clear": UDN_StringClear,		// Initialize a string to empty string, so we can append to it again
		"__concat": UDN_StringConcat,
		"__input": UDN_Input,			//TODO(g): This takes any input as the first arg, and then passes it along, so we can type in new input to go down the pipeline...
		"__input_get": UDN_InputGet,			// Gets information from the input, accessing it like __get
		"__function": UDN_StoredFunction,			//TODO(g): This uses the udn_stored_function.name as the first argument, and then uses the current input to pass to the function, returning the final result of the function.		Uses the web_site.udn_stored_function_domain_id to determine the stored function
		"__execute": UDN_Execute,			//TODO(g): Executes ("eval") a UDN string, assumed to be a "Set" type (Target), will use __input as the Source, and the passed in string as the Target UDN

		"__html_encode": UDN_HtmlEncode,		// Encode HTML symbols so they are not taken as literal HTML


		"__array_append": UDN_ArrayAppend,			// Appends the input into the specified target location (args)

		"__array_divide": UDN_ArrayDivide,			//TODO(g): Breaks an array up into a set of arrays, based on a divisor.  Ex: divide=4, a 14 item array will be 4 arrays, of 4/4/4/2 items each.
		"__array_map_remap": UDN_ArrayMapRemap,			//TODO(g): Takes an array of maps, and makes a new array of maps, based on the arg[0] (map) mapping (key_new=key_old)


		"__map_key_delete": UDN_MapKeyDelete,			// Each argument is a key to remove
		"__map_key_set": UDN_MapKeySet,			// Sets N keys, like __format, but with no formatting
		"__map_copy": UDN_MapCopy,			// Make a copy of the current map, in a new map
		"__map_update": UDN_MapUpdate,			// Input map has fields updated with arg0 map

		"__render_data": UDN_RenderDataWidgetInstance,			// Renders a Data Widget Instance:  arg0 = web_data_widget_instance.id, arg1 = widget_instance map update

		"__json_decode": UDN_JsonDecode,			// Decode JSON
		"__json_encode": UDN_JsonEncode,			// Encode JSON

		"__data_get": UDN_DataGet,					// Dataman Get
		"__data_set": UDN_DataSet,					// Dataman Set
		"__data_filter": UDN_DataFilter,			// Dataman Filter

		"__compare_equal": UDN_CompareEqual,		// Compare equality, takes 2 args and compares them.  Returns 1 if true, 0 if false.  For now, avoiding boolean types...
		"__compare_not_equal": UDN_CompareNotEqual,		// Compare equality, takes 2 args and compares them.  Returns 1 if true, 0 if false.  For now, avoiding boolean types...

		"__ddd_render": UDN_DddRender,			// DDD Render.current: the JSON Dialog Form data for this DDD position.  Uses __ddd_get to get the data, and ___ddd_move to change position.

		"__login": UDN_Login,				// Login through LDAP

		//TODO(g): I think I dont need this, as I can pass it to __ddd_render directly
		//"__ddd_move": UDN_DddMove,				// DDD Move position.current.x.y:  Takes X/Y args, attempted to move:  0.1.1 ^ 0.1.0 < 0.1 > 0.1.0 V 0.1.1
		//"__ddd_get": UDN_DddGet,					// DDD Get.current.{}
		//"__ddd_set": UDN_DddSet,					// DDD Set.current.{}
		//"__ddd_delete": UDN_DddDelete,			// DDD Delete.current: Delete the current item (and all it's sub-items).  Append will be used with __ddd_set/move

		//"__increment": UDN_Increment,				// Increment value
		//"__decrement": UDN_Decrement,				// Decrement value
		//"__split": UDN_StringSplit,				// Split a string into an array on a separator string
		//"__join": UDN_StringJoin,					// Join an array into a string on a separator string
		//"__render_page": UDN_RenderPage,			// Render a page, and return it's widgets so they can be dynamically updated

		// New

		//"__array_append": UDN_ArrayAppend,			//TODO(g): Appends a element onto an array.  This can be used to stage static content so its not HUGE on one line too...

		//"__map_update_prefix": UDN_MapUpdatePrefix,			//TODO(g): Merge a the specified map into the input map, with a prefix, so we can do things like push the schema into the row map, giving us access to the field names and such
		//"__map_clear": UDN_MapClear,			//TODO(g): Clears everything in a map "bucket", like: __map_clear.'temp'

		//"__function_domain": UDN_StoredFunctionDomain,			//TODO(g): Just like function, but allows specifying the udn_stored_function_domain.id as well, so we can use different namespaces.
		//"__capitalize": UDN_StringCapitalize,			//TODO(g): This capitalizes words, title-style
		//"__pluralize": UDN_StringPluralize,			//TODO(g): This pluralizes words, or tries to at least
		//"__starts_with": UDN_StringStartsWith,			//TODO(g): Returns bool if a string starts with the specified arg[0] string
		//"__ends_with": UDN_StringEndsWith,			//TODO(g): Returns bool if a string starts with the specified arg[0] string
		//"__split": UDN_StringSplit,			//TODO(g): Split a string on a value, with a maximum number of splits, and the slice of which to use, with a join as optional value.   The args go:  0) separate to split on,  2)  maximum number of times to split (0=no limit), 3) location to write the split out data (ex: `temp.action.fieldname`) , 3) index of the split to pull out (ex: -1, 0, 1, for the last, first or second)  4) optional: the end of the index to split on, which creates an array of items  5) optional: the join value to join multiple splits on (ex: `_`, which would create a string like:  `second_third_forth` out of a [1,4] slice)
		//"__get_session_data": UDN_SessionDataGet,			//TODO(g): Get something from a safe space in session data (cannot conflict with internal data)
		//"__set_session_data": UDN_SessionDataGet,			//TODO(g): Set something from a safe space in session data (cannot conflict with internal data)
		//"__continue": UDN_IterateContinue,		// Skip to next iteration
		// -- Dont think I need this -- //"__break": UDN_IterateBreak,				//TODO(g): Break this iteration, we are done.  Is this needed?  Im not sure its needed, and it might suck

		// Allows safe concurrency operations...
		//"__set_temp": UDN_Set_Temp,		// Sets a temporary variable.  Is safe for this sequence, and cannot conflict with our UDN setting the same names as temp vars in other threads
		//"__get_temp": UDN_Set_Temp,		// Gets a temporary variable.  Is safe for this sequence, and cannot conflict with our UDN setting the same names as temp vars in other threads
	}

	PartTypeName = map[int]string{
		int(part_unknown): "Unknown",
		int(part_function): "Function",
		int(part_item): "Item",
		int(part_string): "String",
		int(part_compound): "Compound",
		int(part_list): "List",
		int(part_map): "Map",
		int(part_map_key): "Map Key",
	}
}

func init() {
	fmt.Printf("Initializing Yudien\n")

	PgConnect = ReadPathData("data/opsdb.connect")

	// Initialize UDN
	InitUdn()
}


func Lock(lock string) {
	// This must lock things globally.  Global lock server required, only for this Customer though, since "global" can be customer oriented.
	fmt.Printf("Locking: %s\n", lock)

	// Acquire a lock, wait forever until we get it.  Pass in a request UUID so I can see who has the lock.
}

func Unlock(lock string) {
	// This must lock things globally.  Global lock server required, only for this Customer though, since "global" can be customer oriented.
	fmt.Printf("Unlocking: %s\n", lock)

	// Release a lock.  Should we ensure we still had it?  Can do if we gave it our request UUID
}


func ProcessSchemaUDNSet(db *sql.DB, udn_schema map[string]interface{}, udn_data_json string, udn_data map[string]interface{}) interface{} {
	fmt.Printf("ProcessSchemaUDNSet: JSON:\n%s\n\n", udn_data_json)

	var result interface{}

	if udn_data_json != "" {
		// Extract the JSON into a list of list of lists (2), which gives our execution blocks, and UDN pairs (Source/Target)
		udn_execution_group := UdnExecutionGroup{}

		// Decode the JSON data for the widget data
		err := json.Unmarshal([]byte(udn_data_json), &udn_execution_group.Blocks)
		if err != nil {
			log.Panic(err)
		}

		// Ensure there is a Function Stack
		if udn_data["__function_stack"] == nil {
			udn_data["__function_stack"] = make([]map[string]interface{}, 0)
		}

		// Add the new stack to the stack
		new_function_stack := make(map[string]interface{})
		new_function_stack["uuid"] = ksuid.New().String()
		udn_data["__function_stack"] = append(udn_data["__function_stack"].([]map[string]interface{}), new_function_stack)

		//fmt.Printf("UDN Execution Group: %v\n\n", udn_execution_group)

		// Process all the UDN Execution blocks
		//TODO(g): Add in concurrency, right now it does it all sequentially
		for _, udn_group := range udn_execution_group.Blocks {
			for _, udn_group_block := range udn_group {
				result = ProcessUDN(db, udn_schema, udn_group_block[0], udn_group_block[1], udn_data)
			}
		}

		// Remove the latest function stack, that we just put on
		udn_data["__function_stack"] = udn_data["__function_stack"].([]map[string]interface{})[0:len(udn_data["__function_stack"].([]map[string]interface{}))]

		//TODO(g): Remove the udn_data["__temp_UUID"] data, so it doesnt just polluate up the udn_data space?  Once we have returned, we dont need it anymore...
		//CleanUdnTempSpace(new_function_stack["uuid"])
		//
	} else {
		fmt.Print("UDN Execution Group: None\n\n")
	}

	return result
}

// Prepare UDN processing from schema specification -- Returns all the data structures we need to parse UDN properly
func PrepareSchemaUDN(db *sql.DB) map[string]interface{} {
	// Config
	sql := "SELECT * FROM udn_config ORDER BY name"

	result := Query(db, sql)

	udn_config_map := make(map[string]interface{})

	// Add base_page_widget entries to page_map, if they dont already exist
	for _, value := range result {
		//fmt.Printf("UDN Config: %s = \"%s\"\n", value.Map["name"], value.Map["sigil"])

		// Save the config value and sigil
		//udn_config_map[string(value.Map["name"].(string))] = string(value.Map["sigil"].(string))

		// Create the TextTemplateMap
		udn_config_map[string(value["name"].(string))] = string(value["sigil"].(string))
	}

	//fmt.Printf("udn_config_map: %v\n", udn_config_map)

	// Function
	sql = "SELECT * FROM udn_function ORDER BY name"

	result = Query(db, sql)

	udn_function_map := make(map[string]string)
	udn_function_id_alias_map := make(map[int64]string)
	udn_function_id_function_map := make(map[int64]string)

	// Add base_page_widget entries to page_map, if they dont already exist
	for _, value := range result {
		//fmt.Printf("UDN Function: %s = \"%s\"\n", value.Map["alias"], value.Map["function"])

		// Save the config value and sigil
		udn_function_map[string(value["alias"].(string))] = string(value["function"].(string))
		udn_function_id_alias_map[value["_id"].(int64)] = string(value["alias"].(string))
		udn_function_id_function_map[value["_id"].(int64)] = string(value["function"].(string))
	}

	//fmt.Printf("udn_function_map: %v\n", udn_function_map)
	//fmt.Printf("udn_function_id_alias_map: %v\n", udn_function_id_alias_map)
	//fmt.Printf("udn_function_id_function_map: %v\n", udn_function_id_function_map)

	// Group
	sql = "SELECT * FROM udn_group ORDER BY name"

	result = Query(db, sql)

	//udn_group_map := make(map[string]*TextTemplateMap)
	udn_group_map := make(map[string]interface{})

	// Add base_page_widget entries to page_map, if they dont already exist
	for _, value := range result {
		//fmt.Printf("UDN Group: %s = Start: \"%s\"  End: \"%s\"  Is Key Value: %v\n", value.Map["name"], value.Map["sigil"])

		udn_group_map[string(value["name"].(string))] = make(map[string]interface{})
	}

	// Load the user functions
	sql = "SELECT * FROM udn_stored_function ORDER BY name"

	result = Query(db, sql)

	//udn_group_map := make(map[string]*TextTemplateMap)
	udn_stored_function := make(map[string]interface{})

	// Add base_page_widget entries to page_map, if they dont already exist
	for _, value := range result {
		udn_stored_function[string(value["name"].(string))] = make(map[string]interface{})
	}


	//fmt.Printf("udn_group_map: %v\n", udn_group_map)

	// Pack a result map for return
	result_map := make(map[string]interface{})

	result_map["function_map"] = udn_function_map
	result_map["function_id_alias_map"] = udn_function_id_alias_map
	result_map["function_id_function_map"] = udn_function_id_function_map
	result_map["group_map"] = udn_group_map
	result_map["config_map"] = udn_config_map
	result_map["stored_function"] = udn_stored_function

	// By default, do not debug this request
	result_map["udn_debug"] = false

	// Debug information, for rendering the debug output
	UdnDebugReset(result_map)

	fmt.Printf("=-=-=-=-= UDN Schema Created =-=-=-=-=\n")

	return result_map
}

// Pass in a UDN string to be processed - Takes function map, and UDN schema data and other things as input, as it works stand-alone from the application it supports
func ProcessUDN(db *sql.DB, udn_schema map[string]interface{}, udn_value_source string, udn_value_target string, udn_data map[string]interface{}) interface{} {
	UdnLog(udn_schema, "\n\nProcess UDN: Source:  %s   Target:  %s\n\n", udn_value_source, udn_value_target)

	udn_source := ParseUdnString(db, udn_schema, udn_value_source)
	udn_target := ParseUdnString(db, udn_schema, udn_value_target)

	//UdnLog(udn_schema, "\n-------DESCRIPTION: SOURCE-------\n\n%s\n", DescribeUdnPart(udn_source))

	UdnDebugIncrementChunk(udn_schema)
	UdnLogHtml(udn_schema, "-------UDN: SOURCE-------\n%s\n", udn_value_source)
	UdnLog(udn_schema, "-------BEGIN EXECUTION: SOURCE-------\n\n")


	var source_input interface{}

	// Execute the Source UDN
	source_result := ExecuteUdn(db, udn_schema, udn_source, source_input, udn_data)

	UdnLog(udn_schema, "-------RESULT: SOURCE: %v\n\n", SnippetData(source_result, 300))

	//UdnLog(udn_schema, "\n-------DESCRIPTION: TARGET-------\n\n%s", DescribeUdnPart(udn_target))

	UdnDebugIncrementChunk(udn_schema)
	UdnLogHtml(udn_schema, "-------UDN: TARGET-------\n%s\n", udn_value_target)
	UdnLog(udn_schema, "-------BEGIN EXECUTION: TARGET-------\n\n")

	// Execute the Target UDN
	target_result := ExecuteUdn(db, udn_schema, udn_target, source_result, udn_data)

	UdnLog(udn_schema, "\n-------END EXECUTION: TARGET-------\n\n")

	// If we got something from our target result, return it
	if target_result != nil {
		UdnLog(udn_schema, "-------RETURNING: TARGET: %v\n\n", SnippetData(target_result, 300))
		return target_result
	} else {
		// Else, return our source result.  It makes more sense to return Target since it ran last, if it exists...
		UdnLog(udn_schema, "-------RETURNING: SOURCE: %v\n\n", SnippetData(target_result, 300))
		return source_result
	}
}

func ProcessSingleUDNTarget(db *sql.DB, udn_schema map[string]interface{}, udn_value_target string, input interface{}, udn_data map[string]interface{}) interface{} {
	UdnLog(udn_schema, "\n\nProcess Single UDN: Target:  %s  Input: %s\n\n", udn_value_target, SnippetData(input, 80))

	udn_target := ParseUdnString(db, udn_schema, udn_value_target)

	target_result := ExecuteUdn(db, udn_schema, udn_target, input, udn_data)

	UdnLog(udn_schema, "-------RETURNING: TARGET: %v\n\n", SnippetData(target_result, 300))
	return target_result
}


func ProcessUdnArguments(db *sql.DB, udn_schema map[string]interface{}, udn_start *UdnPart, input interface{}, udn_data map[string]interface{}) []interface{} {
	if udn_start.Children.Len() > 0 {
		UdnLog(udn_schema, "Processing UDN Arguments: %s [%s]  Starting: Arg Count: %d \n", udn_start.Value, udn_start.Id, udn_start.Children.Len())
	}

	// Argument list
	args := make([]interface{}, 0)

	// Look through the children, adding them to the args, as they are processed.
	//TODO(g): Do the accessors too, but for now, all of them will be functions, so optimizing for that case initially

	for child := udn_start.Children.Front(); child != nil; child = child.Next() {
		arg_udn_start := child.Value.(*UdnPart)

		if arg_udn_start.PartType == part_compound {
			// In a Compound part, the NextUdnPart is the function (currently)
			//TODO(g): This could be anything in the future, but at this point it should always be a function in a compound...  As it's a sub-statement.
			if arg_udn_start.NextUdnPart != nil {
				//UdnLog(udn_schema, "-=-=-= Args Execute from Compound -=-=-=-\n")
				arg_result := ExecuteUdn(db, udn_schema, arg_udn_start.NextUdnPart, input, udn_data)
				//UdnLog(udn_schema, "-=-=-= Args Execute from Compound -=-=-=-  RESULT: %T: %v\n", arg_result, arg_result)
				//fmt.Printf("Compound Part: %s\n", DescribeUdnPart(arg_udn_start.NextUdnPart))
				//fmt.Printf("Compound Parent: %s\n", DescribeUdnPart(arg_udn_start))

				args = AppendArray(args, arg_result)
			} else {
				//UdnLog(udn_schema, "  UDN Args: Skipping: No NextUdnPart: Children: %d\n\n", arg_udn_start.Children.Len())
				//UdnLog(udn_schema, "  UDN Args: Skipping: No NextUdnPart: Value: %v\n\n", arg_udn_start.Value)
			}
		} else if arg_udn_start.PartType == part_function {
			//UdnLog(udn_schema, "-=-=-= Args Execute from Function -=-=-=-\n")
			arg_result := ExecuteUdn(db, udn_schema, arg_udn_start, input, udn_data)

			args = AppendArray(args, arg_result)
		} else if arg_udn_start.PartType == part_map {
			// Take the value as a literal (string, basically, but it can be tested and converted)

			arg_result_result := make(map[string]interface{})

			//UdnLog(udn_schema, "--Starting Map Arg--\n\n")

			// Then we populate it with data, by processing each of the keys and values
			//TODO(g): Will first assume all keys are strings.  We may want to allow these to be dynamic as well, letting them be set by UDN, but forcing to a string afterwards...
			for child := arg_udn_start.Children.Front(); child != nil; child = child.Next() {
				key := child.Value.(*UdnPart).Value

				//ORIGINAL:
				udn_part_value := child.Value.(*UdnPart).Children.Front().Value.(*UdnPart)
				//udn_part_result := ExecuteUdnPart(db, udn_schema, udn_part_value, input, udn_data)
				udn_part_result := ExecuteUdnCompound(db, udn_schema, udn_part_value, input, udn_data)
				arg_result_result[key] = udn_part_result.Result


				UdnLog(udn_schema, "--  Map:  Key: %s  Value: %v (%T)--\n\n", key, udn_part_result.Result, udn_part_result.Result)
			}
			//UdnLog(udn_schema, "--Ending Map Arg--\n\n")

			args = AppendArray(args, arg_result_result)
		} else if arg_udn_start.PartType == part_list {
			// Take each list item and process it as UDN, to get the final result for this arg value
			// Populate the list
			//list_values := list.New()
			array_values := make([]interface{}, 0)

			//TODO(g): Convert to an array.  I tried it naively, and it didnt work, so it needs a little more work than just these 2 lines...
			//list_values := make([]interface{}, 0)

			// Then we populate it with data, by processing each of the keys and values
			for child := arg_udn_start.Children.Front(); child != nil; child = child.Next() {
				udn_part_value := child.Value.(*UdnPart)

				UdnLog(udn_schema, "List Arg Eval: %v\n", udn_part_value)

				udn_part_result := ExecuteUdnPart(db, udn_schema, udn_part_value, input, udn_data)
				//list_values.PushBack(udn_part_result.Result)
				array_values = AppendArray(array_values, udn_part_result.Result)
			}

			//UdnLog(udn_schema, "  UDN Argument: List: %v\n", SprintList(*list_values))

			//args = AppendArray(args, list_values)
			args = AppendArray(args, array_values)
		} else {
			args = AppendArray(args, arg_udn_start.Value)
		}
	}

	// Only log if we have something to say, otherwise its just noise
	if len(args) > 0 {
		UdnLog(udn_schema, "Processing UDN Arguments: %s [%s]  Result: %s\n", udn_start.Value, udn_start.Id, SnippetData(args, 400))
	}

	return args
}

func UdnDebugWriteHtml(udn_schema map[string]interface{}) string {
	fmt.Printf("\n\n\n\n-=-=-=-=-=- UDN Debug Write HTML -=-=-=-=-=-\n\n\n\n")

	//TODO(g): Make this unique, time in milliseconds should be OK (and sequential), so we can have more than one.  Then deal with cleanup.  And make a sub directory...
	output_path := "/tmp/udn_debug_log.html"

	// Process any remaining HTML chunk as well
	UdnDebugIncrementChunk(udn_schema)

	err := ioutil.WriteFile(output_path, []byte(udn_schema["debug_output_html"].(string)), 0644)
	if err != nil {
		panic(err)
	}

	// Clear the schema info
	//TODO(g): This only works for concurrency at the moment because I get the udn_schema every request, which is wasteful.  So work that out...
	UdnDebugReset(udn_schema)

	return output_path
}

func UdnDebugReset(udn_schema map[string]interface{}) {
	fmt.Printf("\n\n\n\n-=-=-=-=-=- UDN Debug Reset -=-=-=-=-=-\n\n\n\n")

	udn_schema["debug_log"] = ""
	udn_schema["debug_log_count"] = 0
	udn_schema["debug_html_chunk_count"] = 0
	udn_schema["debug_html_chunk"] = ""
	udn_schema["debug_output"] = ""
	udn_schema["debug_output_html"] = `
		<head>
			<script src="https://ajax.googleapis.com/ajax/libs/jquery/3.2.1/jquery.min.js">
			</script>
			<script>
			function ToggleDisplay(element_id) {
				var current_display = $('#'+element_id).css('display');
				if (current_display == 'none') {
					$('#'+element_id).css('display', 'block');
					//alert('Setting ' + element_id + ' to BLOCK == Current: ' + current_display)
				}
				else {
					$('#'+element_id).css('display', 'none');
					//alert('Setting ' + element_id + ' to NONE == Current: ' + current_display)
				}
			}
			</script>
		</head>
		`

}

func UdnDebugIncrementChunk(udn_schema map[string]interface{}) {
	current := udn_schema["debug_html_chunk_count"].(int)
	current++
	udn_schema["debug_html_chunk_count"] = current

	// Update the output with the current Debug Log (and clear it, as it's temporary).  This ensures anything previously undated, gets updated.
	UdnDebugUpdate(udn_schema)

	// Wrap anything we have put into our current HTML chunk, and write it to the HTML Output
	if udn_schema["debug_html_chunk"] != "" {
		// Render our HTML chunk in a hidden DIV, with a button to toggle visibility
		html_output := fmt.Sprintf("<button onclick=\"ToggleDisplay('debug_chunk_%d')\">Statement %d</button><br><br><div id=\"debug_chunk_%d\" style=\"display: none\">%s</div>\n", current, current, current, udn_schema["debug_html_chunk"])

		udn_schema["debug_output_html"] = udn_schema["debug_output_html"].(string) + html_output

		// Clear the chunk
		udn_schema["debug_html_chunk"] = ""
	}
}

func UdnDebug(udn_schema map[string]interface{}, input interface{}, button_label string, message string) {
	if Debug_Udn || udn_schema["udn_debug"] == true {
		// Increment the number of times we have done this, so we have unique debug log sections
		debug_log_count := udn_schema["debug_log_count"].(int)
		debug_log_count++
		udn_schema["debug_log_count"] = debug_log_count

		// Update the output with the current Debug Log (and clear it, as it's temporary)
		UdnDebugUpdate(udn_schema)
		// Render our input, and current UDN Data as well
		html_output := fmt.Sprintf("<pre>%s</pre><button onclick=\"ToggleDisplay('debug_state_%d')\">%s</button><br><br><div id=\"debug_state_%d\" style=\"display: none\">\n", HtmlClean(message), debug_log_count, button_label, debug_log_count)
		udn_schema["debug_html_chunk"] = udn_schema["debug_html_chunk"].(string) + html_output

		// Input
		switch input.(type) {
		case string:
			udn_schema["debug_html_chunk"] = udn_schema["debug_html_chunk"].(string) + "<pre>" + HtmlClean(input.(string)) + "</pre>"
		default:
			input_output, _ := json.MarshalIndent(input, "", "  ")
			//input_output := fmt.Sprintf("%v", input)	// Tried this to increase performance, this is not the bottleneck...
			udn_schema["debug_html_chunk"] = udn_schema["debug_html_chunk"].(string) + "<pre>" + HtmlClean(string(input_output)) + "</pre>"
		}

		// Close the DIV tag
		udn_schema["debug_html_chunk"] = udn_schema["debug_html_chunk"].(string) + "</div>"

	}
}

func UdnDebugUpdate(udn_schema map[string]interface{}) {
	debug_log_count := udn_schema["debug_log_count"].(int)

	// If we have anything in our UDN Debug Log, lets put it into a DIV we can hide, and clear it out, so we collect them in pieces
	if udn_schema["debug_log"] != "" {
		// Append to our raw output
		udn_schema["debug_output"] = udn_schema["debug_output"].(string) + udn_schema["debug_log"].(string)

		// Append to our HTML output
		html_output := fmt.Sprintf("<button onclick=\"ToggleDisplay('debug_log_%d')\">Debug</button><br><pre id=\"debug_log_%d\" style=\"display: none\">%s</pre>\n", debug_log_count, debug_log_count, HtmlClean(udn_schema["debug_log"].(string)))
		udn_schema["debug_html_chunk"] = udn_schema["debug_html_chunk"].(string) + html_output

		// Clear the debug log, as we have put it into the debug_output and debug_output_html
		udn_schema["debug_log"] = ""
	}
}


func UdnLog(udn_schema map[string]interface{}, format string, args ...interface{}) {
	// Format the incoming Printf args, and print them
	output := fmt.Sprintf(format, args...)
	if Debug_Udn || udn_schema["udn_debug"] == true {
		fmt.Print(output)
	}

	// Append the output into our udn_schema["debug_log"], where we keep raw logs, before wrapping them up for debugging visibility purposes
	udn_schema["debug_log"] = udn_schema["debug_log"].(string) + output
}

func UdnLogHtml(udn_schema map[string]interface{}, format string, args ...interface{}) {
	// Format the incoming Printf args, and print them
	output := fmt.Sprintf(format, args...)
	fmt.Print(output)

	// Append the output into our udn_schema["debug_log"], where we keep raw logs, before wrapping them up for debugging visibility purposes
	udn_schema["debug_log"] = udn_schema["debug_log"].(string) + output
	// Append to HTML as well, so it shows up.  This is a convenience function for this reason.  Headers and stuff.
	udn_schema["debug_output_html"] = udn_schema["debug_output_html"].(string) + "<pre>" + HtmlClean(output) + "</pre>"
}

// Execute a single UDN (Soure or Target) and return the result
//NOTE(g): This function does not return UdnPart, because we want to get direct information, so we return interface{}
func ExecuteUdn(db *sql.DB, udn_schema map[string]interface{}, udn_start *UdnPart, input interface{}, udn_data map[string]interface{}) interface{} {
	// Process all our arguments, Executing any functions, at all depths.  Furthest depth first, to meet dependencies

	UdnLog(udn_schema, "\nExecuteUDN: %s [%s]  Args: %d  Input: %s\n", udn_start.Value, udn_start.Id, udn_start.Children.Len(), SnippetData(input, 40))

	// In case the function is nil, just pass through the input as the result.  Setting it here because we need this declared in function-scope
	var result interface{}

	// If this is a real function (not an end-block nil function)
	if UdnFunctions[udn_start.Value] != nil {
		udn_result := ExecuteUdnPart(db, udn_schema, udn_start, input, udn_data)
		result = udn_result.Result

		// If we have more to process, do it
		if udn_result.NextUdnPart != nil {
			UdnLog(udn_schema, "ExecuteUdn: Flow Control: JUMPING to NextUdnPart: %s [%s]\n", udn_result.NextUdnPart.Value, udn_result.NextUdnPart.Id)
			// Our result gave us a NextUdnPart, so we can assume they performed some execution flow control themeselves, we will continue where they told us to
			result = ExecuteUdn(db, udn_schema, udn_result.NextUdnPart, result, udn_data)
		} else if udn_start.NextUdnPart != nil {
			UdnLog(udn_schema, "ExecuteUdn: Flow Control: STEPPING to NextUdnPart: %s [%s]\n", udn_start.NextUdnPart.Value, udn_start.NextUdnPart.Id)
			// We have a NextUdnPart and we didnt recieve a different NextUdnPart from our udn_result, so execute sequentially
			result = ExecuteUdn(db, udn_schema, udn_start.NextUdnPart, result, udn_data)
		}
	} else {
		// Set the result to our input, because we got a nil-function, which doesnt change the result
		result = input
	}

	// If the UDN Result is a list, convert it to an array, as it's easier to read the output
	//TODO(g): Remove all the list.List stuff, so everything is an array.  Better.
	result_type_str := fmt.Sprintf("%T", result)
	if result_type_str == "*list.List" {
		result = GetResult(result, type_array)
	}

	UdnLog(udn_schema, "ExecuteUDN: End Function: %s [%s]: Result: %s\n\n", udn_start.Value, udn_start.Id, SnippetData(result, 40))

	// Return the result directly (interface{})
	return result
}


// Execute a single UdnPart.  This is necessary, because it may not be a function, it might be a Compound, which has a function inside it.
//		At the top level, this is not necessary, but for flow control, we need to wrap this so that each Block Executor doesnt need to duplicate logic.
//NOTE(g): This function must return a UdnPart, because it is necessary for Flow Control (__iterate, etc)
func ExecuteUdnPart(db *sql.DB, udn_schema map[string]interface{}, udn_start *UdnPart, input interface{}, udn_data map[string]interface{}) UdnResult {
	//UdnLog(udn_schema, "Executing UDN Part: %s [%s]\n", udn_start.Value, udn_start.Id)

	// Process the arguments
	args := ProcessUdnArguments(db, udn_schema, udn_start, input, udn_data)

	UdnDebug(udn_schema, input, "View Input", fmt.Sprintf("Execute UDN Part: %s: %v", udn_start.Value, SnippetData(args, 300)))

	// Store this so we can access it if we want
	//TODO(g): Is this required?  Is it the best place for it?  Investiage at some point...  Not sure re-reading it.
	udn_data["arg"] = args


	// What we return, unified return type in UDN
	udn_result := UdnResult{}

	if udn_start.PartType == part_function {
		if UdnFunctions[udn_start.Value] != nil {
			// Execute a function
			UdnLog(udn_schema, "Executing: %s [%s]   Args: %v\n", udn_start.Value, udn_start.Id, SnippetData(args, 80))

			udn_result = UdnFunctions[udn_start.Value](db, udn_schema, udn_start, args, input, udn_data)
		} else {
			//UdnLog(udn_schema, "Skipping Execution, nil function, result = input: %s\n", udn_start.Value)
			udn_result.Result = input
		}
	} else if udn_start.PartType == part_compound {
		// Execute the first part of the Compound (should be a function, but it will get worked out)
		udn_result = ExecuteUdnPart(db, udn_schema, udn_start.NextUdnPart, input, udn_data)
	} else if udn_start.PartType == part_map {
		map_result := make(map[string]interface{})

		for child := udn_start.Children.Front(); child != nil; child = child.Next() {
			cur_child := *child.Value.(*UdnPart)

			key := cur_child.Value
			value := cur_child.Children.Front().Value.(*UdnPart).Value

			map_result[key] = value
		}

		udn_result.Result = map_result

	} else {
		// We just store the value, if it is not handled as a special case above
		udn_result.Result = udn_start.Value
	}

	//UdnLog(udn_schema, "=-=-=-=-= Executing UDN Part: End: %s [%s] Full Result: %v\n\n", udn_start.Value, udn_start.Id, udn_result.Result)	// DEBUG

	UdnDebug(udn_schema, udn_result.Result, "View Output", "")

	return udn_result
}


// Execute a UDN Compound
func ExecuteUdnCompound(db *sql.DB, udn_schema map[string]interface{}, udn_start *UdnPart, input interface{}, udn_data map[string]interface{}) UdnResult {
	udn_result := UdnResult{}

	if udn_start.NextUdnPart != nil {
		// If this is a Compound, process it
		udn_current := udn_start.NextUdnPart

		done := false


		for !done {
			//fmt.Printf("Execute UDN Compound: %s\n", DescribeUdnPart(udn_current))
			//fmt.Printf("Execute UDN Compound: Input: %s\n", SnippetData(input, 60))

			udn_result = ExecuteUdnPart(db, udn_schema, udn_current, input, udn_data)
			input = udn_result.Result

			if udn_current.NextUdnPart == nil {
				done = true
				//fmt.Print("  UDN Compound: Finished\n")
			} else {
				udn_current = udn_current.NextUdnPart
				//fmt.Printf("  Next UDN Compound: %s\n", udn_current.Value)
			}
		}
	} else {
		// If we arent a compount, return the value
		udn_result.Result = udn_start.Value
	}

	return udn_result
}





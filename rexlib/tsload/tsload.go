package tsload

import (
	"fmt"

	"os/exec"
	"path/filepath"

	"reflect"

	"strconv"

	"encoding/json"
)

const (
	defaultTPDispatcher      = "first-free"
	defaultWLRQSched         = "iat"
	defaultWLRQSDistribution = "exponential"
)

var tsloadPath string

func SetTSLoadPath(path string) {
	tsloadPath = path
}

func CreateTSExperimentCommand(expDir string) (cmd *exec.Cmd) {
	cmd = exec.Command(filepath.Join(tsloadPath, "bin", "tsexperiment"),
		"-e", ".", "run")
	cmd.Dir = expDir

	return
}

type ThreadPoolDisp struct {
	Type string `json:"type" opt:"tpd|disp,opt"`

	// fill-up params are not currently supported
}

type ThreadPool struct {
	// Never serialized in experiment json (map key is used). If not set,
	// name is generated
	Name string `json:"-" arg:"1,opt"`

	// Number of threads as specified by user
	NumThreads int `json:"num_threads" opt:"j|nt"`

	// Taken from incident params
	Quantum int64 `json:"quantum"`

	Dispatcher ThreadPoolDisp `json:"disp"`

	// sched & discard are not currently supported
}

type WLSteps struct {
	// Same number of requests every step
	NumRequests int `opt:"r,opt" json:"num_requests,omitempty"`
	NumSteps    int `opt:"s,opt" json:"num_steps,omitempty"`

	// Series-based steps (but passed as arguments here)
	Series []int `arg:"1,opt" json:"series,omitempty"`
}

type WLRQSched struct {
	Type         string `json:"type" opt:"rqs|sched,opt"`
	Distribution string `json:"distribution" opt:"d,opt"`

	Scope       float32 `json:"scope,omitempty" opt:"scope,opt"`
	Shape       int     `json:"shape,omitempty" opt:"shape,opt"`
	Covariation float32 `json:"covar,omitempty" opt:"covar,opt"`
}

type WLParamRandGen struct {
	RandGenClass string `json:"class" opt:"rg|randgen,opt"`
	Seed         int64  `json:"seed" opt:"rgseed,opt"`
}

type WLParamRandVar struct {
	RandVarClass string `json:"class" opt:"rv|randvar,opt"`

	Rate  float32 `json:"rate,omitempty" opt:"r|rate,opt"`
	Shape int     `json:"shape,omitempty" opt:"shape,opt"`

	Min float32 `json:"min,omitempty" opt:"min,opt"`
	Max float32 `json:"max,omitempty" opt:"max,opt"`

	Mean   float32 `json:"mean,omitempty" opt:"m|mean,opt"`
	StdDev float32 `json:"stddev,omitempty" opt:"sd|stddev,opt"`
}

type WLParam struct {
	// Is parameter should be encoded as integer
	IsInteger bool `opt:"i,opt"`

	// Probability value (for probability map)
	Probability float32 `opt:"p,opt"`

	RandGen WLParamRandGen
	RandVar WLParamRandVar

	Name string `arg:"1"`

	// List of values (used for valarray for probability maps). Parsed
	// as integers if IsInteger is set by interpretValues(). Optional for
	// randomly generated/variated parameters
	Values []string `arg:"2,opt"`
}

type WLParameters struct {
	Params []*WLParam
}

type WLParametersChunk struct {
	Params []*WLParam
}

type jsonPMapEntry struct {
	Probability float32     `json:"probability"`
	Value       interface{} `json:"value,omitempty"`
	ValArray    interface{} `json:"valarray,omitempty"`
}

type jsonParamValue struct {
	RandGen        *WLParamRandGen `json:"randgen,omitempty"`
	RandVar        *WLParamRandVar `json:"randvar,omitempty"`
	ProbabilityMap []jsonPMapEntry `json:"pmap,omitempty"`
}

type Workload struct {
	Name string `json:"-" arg:"1"`
	Type string `json:"wltype" arg:"2"`

	// Name of the threadpool. If not set, name of the only threadpool is used
	ThreadPool string `json:"threadpool" arg:"3,opt"`

	RQSched    WLRQSched    `json:"rqsched"`
	Parameters WLParameters `json:"params"`
}

type Experiment struct {
	Name       string `json:"name"`
	SingleRun  bool   `json:"single_run"`
	GlobalTime int64  `json:"global_time"`

	Steps       map[string]*WLSteps    `json:"steps"`
	ThreadPools map[string]*ThreadPool `json:"threadpools"`
	Workloads   map[string]*Workload   `json:"workloads"`

	quantum int64
}

func NewExperiment(name string, quantum int64) *Experiment {
	return &Experiment{
		Name:      name,
		SingleRun: true,

		Steps:       make(map[string]*WLSteps),
		ThreadPools: make(map[string]*ThreadPool),
		Workloads:   make(map[string]*Workload),

		quantum: quantum,
	}
}

// Spawns threadpool but doesn't insert it. Generates name and other default
// values for threadpool
func (exp *Experiment) NewThreadPool() (tp *ThreadPool) {
	tp = new(ThreadPool)

	tp.Name = fmt.Sprint("tp", len(exp.ThreadPools))
	tp.NumThreads = 1
	tp.Quantum = exp.quantum
	tp.Dispatcher.Type = defaultTPDispatcher

	return
}

// Spawns workload but doesn't insert it. Fills some default values
func (exp *Experiment) NewWorkload() (wl *Workload) {
	wl = new(Workload)

	wl.Name = fmt.Sprint("wl", len(exp.Workloads))

	// Assign default threadpool name to the first threadpool name.
	for tpName, _ := range exp.ThreadPools {
		wl.ThreadPool = tpName
		break
	}

	wl.Parameters.Params = make([]*WLParam, 0)
	wl.RQSched.Type = defaultWLRQSched
	wl.RQSched.Distribution = defaultWLRQSDistribution

	return
}

// Try to interpret values (as integers if needed) ; return raw value for
// serializing as json and optional isArray flag. If conversion fails, no
// conversion is performed to trigger type checking error in tsexperiment
func (param *WLParam) interpretValues() (interface{}, bool) {
	if len(param.Values) == 0 {
		return nil, false
	}

	if param.IsInteger {
		if len(param.Values) > 1 {
			values := make([]int, 0, len(param.Values))
			for _, value := range param.Values {
				iValue, err := strconv.Atoi(value)
				if err != nil {
					break
				}

				values = append(values, iValue)
			}

			if len(values) == len(param.Values) {
				return values, true
			}
		} else {
			iValue, err := strconv.Atoi(param.Values[0])
			if err == nil {
				return iValue, false
			}
		}
	}

	return param.Values, len(param.Values) > 1
}

// experiment.json accepts workload parameters as a very complex map, but
// we want param commands to be used sequentally in
func (params *WLParameters) MarshalJSON() ([]byte, error) {
	rawParams := make(map[string]interface{})
	randomParams := make(map[string]*jsonParamValue)

	for _, param := range params.Params {
		isProbabilityMap := param.Probability != 0.0
		hasRandGen := len(param.RandGen.RandGenClass) > 0
		hasRandVar := len(param.RandVar.RandVarClass) > 0

		if isProbabilityMap || hasRandGen || hasRandVar {
			randParam, ok := randomParams[param.Name]
			if !ok {
				randParam = new(jsonParamValue)
				rawParams[param.Name] = randParam
				randomParams[param.Name] = randParam
			}

			if hasRandGen {
				randParam.RandGen = &param.RandGen
			}
			if hasRandVar {
				randParam.RandVar = &param.RandVar
			}
			if isProbabilityMap {
				pMapEntry := jsonPMapEntry{
					Probability: param.Probability,
				}

				values, isArray := param.interpretValues()
				if isArray {
					pMapEntry.ValArray = values
				} else {
					pMapEntry.Value = values
				}

				randParam.ProbabilityMap = append(randParam.ProbabilityMap, pMapEntry)
			}
		} else {
			// Normal value -- use latest value
			rawParams[param.Name], _ = param.interpretValues()
		}
	}

	return json.Marshal(rawParams)
}

func (params *WLParametersChunk) UnmarshalJSON(data []byte) (err error) {
	// Unmarshal parameters value. If we get raw value, unmarshal it as-is
	// If we got object (distinguishable by stating {, decode complex
	// randomly-generated parameter value

	param := &WLParam{}
	params.Params = append(params.Params, param)

	if data[0] == '{' {
		var jsonParam jsonParamValue
		err = json.Unmarshal(data, &jsonParam)
		if err != nil {
			return
		}

		// Only first param entry (even in probability map) will receive
		// random generator/variator
		if jsonParam.RandGen != nil {
			param.RandGen = *jsonParam.RandGen
		}
		if jsonParam.RandVar != nil {
			param.RandVar = *jsonParam.RandVar
		}
		if len(jsonParam.ProbabilityMap) > 0 {
			for pi, pmapEntry := range jsonParam.ProbabilityMap {
				param.decodePMapEntry(&pmapEntry)

				if pi < len(jsonParam.ProbabilityMap)-1 {
					param = &WLParam{}
					params.Params = append(params.Params, param)
				}
			}
		}

		return
	}

	param.Values = make([]string, 1)
	if data[0] == '"' {
		err = json.Unmarshal(data, &param.Values[0])
	} else {
		var i int64
		err = json.Unmarshal(data, &i)

		param.IsInteger = true
		param.Values[0] = strconv.FormatInt(i, 10)
	}

	return
}

func (params *WLParameters) UnmarshalJSON(data []byte) (err error) {
	// Usually we get only single entry per parameter, but probability map
	// is an exception, so we first unmarshal map of name to chunk (1 or more
	// parameters) then merge them into single array here. WLParametersChunk
	// have special unmarshaling logic.
	rawParams := make(map[string]WLParametersChunk)

	err = json.Unmarshal(data, &rawParams)
	if err != nil {
		return
	}

	params.Params = make([]*WLParam, 0, len(rawParams))
	for paramName, chunk := range rawParams {
		for pi, _ := range chunk.Params {
			chunk.Params[pi].Name = paramName
		}

		params.Params = append(params.Params, chunk.Params...)
	}

	return
}

func (param *WLParam) decodePMapEntry(pmapEntry *jsonPMapEntry) error {
	param.Probability = pmapEntry.Probability
	if pmapEntry.Value != nil {
		if reflect.ValueOf(pmapEntry.ValArray).Type().Kind() == reflect.Float64 {
			param.IsInteger = true
		}
		param.Values = append(param.Values, fmt.Sprint(pmapEntry.Value))
	} else if pmapEntry.ValArray != nil {
		value := reflect.ValueOf(pmapEntry.ValArray)
		if value.Type().Kind() != reflect.Slice {
			return fmt.Errorf("valarray should be an array (encoded as slice)")
		}

		for i := 0; i < value.Len(); i++ {
			v := value.Index(i)
			if v.Type().Kind() == reflect.Float64 {
				param.IsInteger = true
			}

			param.Values = append(param.Values, fmt.Sprint(v.Interface()))
		}
	} else {
		return fmt.Errorf("Probability map should have value/valarray")
	}

	return nil
}

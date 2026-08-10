package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var (
	BaseURL = getNodeURL(1)

	ProposalAddress  = "02cd4e5eb53ea665702042a6ed6d31d616054dc5"
	ProposalPassword = "test"
	// URL Paths
	RPCPort                 = ":50002"
	AdminPort               = ":50003"
	BlockPath               = RPCPort + "/v1/query/block-by-height"
	ChangeParamProposalPath = AdminPort + "/v1/admin/tx-change-param"
	AddVotePath             = AdminPort + "/v1/gov/add-vote"
	// single client
	client = http.Client{
		Timeout: 3 * time.Second,
	}

	nodes = 12
)

func getNodeURL(node int) string {
	return fmt.Sprintf("http://node-%d.p2p.canopy.svc.cluster.local", node)
}

func main() {
	paramSpace, paramKey, paramValue := "consensus", "protocolVersion", "2/0"
	fmt.Printf("--- Generating proposal with values %s/%s: %s", paramSpace, paramKey,
		paramValue)
	proposal, err := GenerateProposal(paramSpace, paramKey, paramValue)
	if err != nil {
		fmt.Println("Error generating proposal:", err)
		return
	}
	fmt.Println("--- Proposal generated successfully")
	fmt.Println("--- Adding votes from nodes 1 to", nodes)
	if err := AddVotes(proposal, 1, nodes); err != nil {
		fmt.Println("Error adding votes:", err)
		return
	}
}

func GenerateProposal(paramSpace, paramKey, paramValue string) ([]byte, error) {
	// get latest height
	var latest BlockResult
	reqLatest, _ := json.Marshal(HeightRequest{Height: 0}) // Height 0 = latest
	bz, err := POST(BaseURL+BlockPath, reqLatest)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(bz, &latest); err != nil {
		return nil, err
	}
	latestHeight := latest.BlockHeader.Height
	// make a request for the proposal
	request := struct {
		Address    string `json:"address"`
		ParamSpace string `json:"paramSpace"`
		ParamKey   string `json:"paramKey"`
		ParamValue string `json:"paramValue"`
		Amount     int    `json:"amount"`
		StartBlock int    `json:"startBlock"`
		EndBlock   int    `json:"endBlock"`
		Memo       string `json:"memo"`
		Fee        int    `json:"fee"`
		Submit     bool   `json:"submit"`
		Password   string `json:"password"`
	}{
		ParamSpace: paramSpace,
		ParamKey:   paramKey,
		ParamValue: paramValue, StartBlock: latestHeight - 1,
		EndBlock: latestHeight + 999,
		Fee:      10000,
		Submit:   false,
		Address:  ProposalAddress,
		Password: ProposalPassword,
	}
	// convert to json
	bz, err = json.Marshal(request)
	if err != nil {
		return nil, err
	}
	return POST(BaseURL+ChangeParamProposalPath, bz)
}

func AddVotes(proposalBz []byte, startNode, endNode int) error {
	successfulVotes := 0
	defer func() {
		fmt.Printf("--- Total successful votes: %d\n", successfulVotes)
	}()
	// generate wrapper
	wrapperBytes, err := json.Marshal(ProposalWrapper{
		Approve:  true,
		Proposal: proposalBz,
	})
	if err != nil {
		return err
	}
	// add votes from nodes
	for i := startNode; i <= endNode; i++ {
		nodeURL := getNodeURL(i)
		_, err := POST(nodeURL+AddVotePath, wrapperBytes)
		if err != nil {
			return fmt.Errorf("error adding vote from node %d: %w", i, err)
		}
		fmt.Printf("--- Vote added from node %d\n", i)
		successfulVotes++
	}

	return nil
}

type HeightRequest struct {
	Height uint64 `json:"height"`
	ID     uint64 `json:"id"`
}

type BlockResult struct {
	BlockHeader struct {
		Height int   `json:"height"`
		Time   int64 `json:"time"`
	} `json:"blockHeader"`
}

type ProposalWrapper struct {
	Approve  bool            `json:"approve"`
	Proposal json.RawMessage `json:"proposal"`
}

func POST(url string, bz []byte) ([]byte, error) {
	// prepare request
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(bz))
	if err != nil {
		return nil, err
	}
	// execute request
	resp, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error execusting request to %s:%d", url, resp.StatusCode)
	}
	// save response
	respBz, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response from: %s:%w", url, err)
	}
	return respBz, nil
}

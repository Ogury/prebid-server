package ogury

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/golang/glog"
	"github.com/prebid/openrtb/v20/openrtb2"

	"github.com/prebid/prebid-server/v2/adapters"
	"github.com/prebid/prebid-server/v2/config"
	"github.com/prebid/prebid-server/v2/errortypes"
	"github.com/prebid/prebid-server/v2/openrtb_ext"
)

type oguryAdapter struct {
	endpoint string
}

func Builder(_ openrtb_ext.BidderName, config config.Adapter, _ config.Server) (adapters.Bidder, error) {
	adapter := &oguryAdapter{
		endpoint: config.Endpoint,
	}
	return adapter, nil
}

func (a oguryAdapter) MakeRequests(request *openrtb2.BidRequest, requestInfo *adapters.ExtraRequestInfo) ([]*adapters.RequestData, []error) {
	glog.Info("Ogury Adapter: request received")

	headers := http.Header{}
	headers.Add("Content-Type", "application/json;charset=utf-8")
	headers.Add("User-Agent", request.Device.UA)

	headers.Add("X-Forwarded-For", request.Device.IP)

	requestJSON, err := json.Marshal(request)
	if err != nil {
		return nil, []error{err}
	}

	requestData := &adapters.RequestData{
		Method:  "POST",
		Uri:     a.endpoint,
		Body:    requestJSON,
		Headers: headers,
		ImpIDs:  openrtb_ext.GetImpIDs(request.Imp),
	}
	glog.Info(string(requestData.Body))

	return []*adapters.RequestData{requestData}, nil

}

func getMediaTypeForBid(impressions []openrtb2.Imp, bid openrtb2.Bid) (openrtb_ext.BidType, error) {
	for _, imp := range impressions {
		if imp.ID == bid.ImpID {
			switch {
			case imp.Banner != nil:
				return openrtb_ext.BidTypeBanner, nil
			case imp.Video != nil:
				return openrtb_ext.BidTypeVideo, nil
			case imp.Native != nil:
				return openrtb_ext.BidTypeNative, nil
			}
		}

	}

	return "", &errortypes.BadServerResponse{
		Message: fmt.Sprintf("Failed to determine media type of impression \"%s\"", bid.ImpID),
	}
}

func (a oguryAdapter) MakeBids(request *openrtb2.BidRequest, _ *adapters.RequestData, responseData *adapters.ResponseData) (*adapters.BidderResponse, []error) {
	glog.Info("Ogury Adapter: response")
	glog.Infof("Status: %v", responseData.StatusCode)
	glog.Infof("Body: %v", string(responseData.Body))
	glog.Infof("Headers: %+v", responseData.Headers)

	if responseData.StatusCode == http.StatusNoContent {
		return nil, nil
	}

	if responseData.StatusCode == http.StatusBadRequest {
		err := &errortypes.BadInput{
			Message: "Unexpected status code: 400. Bad request from publisher. Run with request.debug = 1 for more info.",
		}
		return nil, []error{err}
	}

	if responseData.StatusCode != http.StatusOK {
		err := &errortypes.BadServerResponse{
			Message: fmt.Sprintf("Unexpected status code: %d. Run with request.debug = 1 for more info.", responseData.StatusCode),
		}
		return nil, []error{err}
	}

	var response openrtb2.BidResponse
	if err := json.Unmarshal(responseData.Body, &response); err != nil {
		return nil, []error{err}
	}

	bidResponse := adapters.NewBidderResponseWithBidsCapacity(len(request.Imp))
	bidResponse.Currency = response.Cur
	var errors []error
	for _, seatBid := range response.SeatBid {
		for i, bid := range seatBid.Bid {
			bidType, err := getMediaTypeForBid(request.Imp, bid)
			if err != nil {
				errors = append(errors, err)
				continue
			}
			bidResponse.Bids = append(bidResponse.Bids, &adapters.TypedBid{
				Bid:     &seatBid.Bid[i],
				BidType: bidType,
			})
		}
	}
	if errors != nil {
		return nil, errors
	}

	return bidResponse, nil
}

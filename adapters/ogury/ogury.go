package ogury

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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
	headers := setHeaders(request)

	var errors []error
	var bidderImpExt adapters.ExtImpBidder
	var oguryExtParams openrtb_ext.ImpExtOgury
	for i, imp := range request.Imp {
		// extract Ogury params
		if err := json.Unmarshal(imp.Ext, &bidderImpExt); err != nil {
			return nil, append(errors, &errortypes.BadInput{
				Message: "Bidder extension not provided or can't be unmarshalled",
			})
		}
		if err := json.Unmarshal(bidderImpExt.Bidder, &oguryExtParams); err != nil {
			return nil, append(errors, &errortypes.BadInput{
				Message: "Error while unmarshalling Ogury bidder extension",
			})
		}

		// map Ogury params to top of Ext object
		ext, err := json.Marshal(struct {
			adapters.ExtImpBidder
			openrtb_ext.ImpExtOgury
		}{bidderImpExt, oguryExtParams})
		if err != nil {
			return nil, append(errors, &errortypes.BadInput{
				Message: "Error while marshaling Imp.Ext bidder exension",
			})
		}
		request.Imp[i].Ext = ext

		if oguryExtParams.PublisherID != "" {
			request.Imp[i].TagID = imp.ID
		}
	}

	for i, imp := range request.Imp {
		// Check if imp comes with bid floor amount defined in a foreign currency
		if imp.BidFloor > 0 && imp.BidFloorCur != "" && strings.ToUpper(imp.BidFloorCur) != "USD" {

			// Convert to US dollars
			convertedValue, err := requestInfo.ConvertCurrency(imp.BidFloor, imp.BidFloorCur, "USD")
			if err != nil {
				return nil, []error{err}
			}

			// Update after conversion. All imp elements inside request.Imp are shallow copies
			// therefore, their non-pointer values are not shared memory and are safe to modify.
			// TODO: this probably needs a fix like request.imp[i] =, check
			request.Imp[i].BidFloorCur = "USD"
			request.Imp[i].BidFloor = convertedValue
		}
	}

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

	glog.Info("Ogury Adapter: request received") // TODO: delete this
	glog.Info("[REQUEST] Headers: ", requestData.Headers)
	glog.Info("[REQUEST] Body: ", string(requestData.Body))

	return []*adapters.RequestData{requestData}, nil

}

func setHeaders(request *openrtb2.BidRequest) http.Header {
	headers := http.Header{}
	headers.Add("Content-Type", "application/json;charset=utf-8")
	if request.Device != nil {
		headers.Add("X-Forwarded-For", request.Device.IP)
		headers.Add("User-Agent", request.Device.UA)
		headers.Add("Accept-Language", request.Device.Language)
	}
	return headers

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
	// TODO: delete all the info logs used in development
	glog.Info("Ogury Adapter: response")
	glog.Infof("[RESPONSE] Status: %v", responseData.StatusCode)
	glog.Infof("[RESPONSE] Body: %v", string(responseData.Body))
	glog.Infof("[RESPONSE] Headers: %+v", responseData.Headers)

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

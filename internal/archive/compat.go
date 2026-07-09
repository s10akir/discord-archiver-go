package archive

import (
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/bwmarrin/discordgo"
)

// channelMessagesCompat mirrors discordgo.Session.ChannelMessages while working around
// discordgo v0.29.0 failing on newly introduced Discord component types.
//
// Remove this wrapper and call session.ChannelMessages directly once discordgo can
// unmarshal unknown/future message component types without failing the whole page.
func channelMessagesCompat(session *discordgo.Session, channelID string, limit int, beforeID, afterID, aroundID string) ([]*discordgo.Message, error) {
	uri := discordgo.EndpointChannelMessages(channelID)

	values := url.Values{}
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	if afterID != "" {
		values.Set("after", afterID)
	}
	if beforeID != "" {
		values.Set("before", beforeID)
	}
	if aroundID != "" {
		values.Set("around", aroundID)
	}
	if len(values) > 0 {
		uri += "?" + values.Encode()
	}

	body, err := session.RequestWithBucketID("GET", uri, nil, discordgo.EndpointChannelMessages(channelID))
	if err != nil {
		return nil, err
	}

	return unmarshalMessagesWithoutComponents(body)
}

// unmarshalMessagesWithoutComponents drops components before decoding because
// discordgo.Message does not marshal Components back to JSON anyway.
func unmarshalMessagesWithoutComponents(body []byte) ([]*discordgo.Message, error) {
	var rawMessages []map[string]json.RawMessage
	if err := json.Unmarshal(body, &rawMessages); err != nil {
		return nil, err
	}

	messages := make([]*discordgo.Message, 0, len(rawMessages))
	for _, rawMessage := range rawMessages {
		delete(rawMessage, "components")

		messageBody, err := json.Marshal(rawMessage)
		if err != nil {
			return nil, err
		}

		var message discordgo.Message
		if err := json.Unmarshal(messageBody, &message); err != nil {
			return nil, err
		}
		messages = append(messages, &message)
	}

	return messages, nil
}
